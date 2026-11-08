// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package whereabouts_e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	wbtestclient "github.com/telekom/whereabouts/e2e/client"
	"github.com/telekom/whereabouts/e2e/entities"
	"github.com/telekom/whereabouts/e2e/retrievers"
	"github.com/telekom/whereabouts/e2e/util"
	wbstorage "github.com/telekom/whereabouts/pkg/storage/kubernetes"
)

// The CNI install DaemonSet runs unprivileged by default. CI re-runs this spec
// after `helm upgrade --set cniPrivileged=true` (E2E_CNI_PRIVILEGED=true) and
// `--set seLinuxSpcT=true` (E2E_CNI_SELINUX_SPC_T=true) to cover the opt-outs.
var _ = Describe("CNI install DaemonSet", func() {
	const (
		cniNamespace  = "kube-system"
		cniSelector   = "name=whereabouts"
		testNamespace = "default"
		networkName   = "wa-cni-install"
		ipRange       = "10.131.0.0/24"
	)

	var (
		clientInfo *wbtestclient.ClientInfo
		pods       []*corev1.Pod
	)

	BeforeEach(func() {
		config, err := util.ClusterConfig()
		Expect(err).NotTo(HaveOccurred())
		clientInfo, err = wbtestclient.NewClientInfo(config)
		Expect(err).NotTo(HaveOccurred())
		pods = nil
	})

	AfterEach(func() {
		for _, pod := range pods {
			_ = clientInfo.DeletePod(pod)
		}
	})

	It("runs install-cni with the configured security context, installs the plugin on every node and serves IPAM", func() {
		ctx := context.Background()
		privileged := os.Getenv("E2E_CNI_PRIVILEGED") == "true"
		spcT := os.Getenv("E2E_CNI_SELINUX_SPC_T") == "true"
		By(fmt.Sprintf("expecting privileged=%t seLinuxSpcT=%t", privileged, spcT))

		nodes, err := clientInfo.Client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())

		By("waiting for one ready install-cni pod per node with the expected securityContext")
		var cniPods []corev1.Pod
		Eventually(func(g Gomega) {
			list, err := clientInfo.Client.CoreV1().Pods(cniNamespace).List(ctx, metav1.ListOptions{LabelSelector: cniSelector})
			g.Expect(err).NotTo(HaveOccurred())
			cniPods = cniPods[:0]
			for _, pod := range list.Items {
				if pod.DeletionTimestamp == nil {
					cniPods = append(cniPods, pod)
				}
			}
			g.Expect(cniPods).To(HaveLen(len(nodes.Items)))
			for _, pod := range cniPods {
				g.Expect(podReady(&pod)).To(BeTrue(), "pod %s not ready", pod.Name)
				g.Expect(pod.Spec.Containers).To(HaveLen(1))
				expectCNISecurityContext(g, pod.Spec.Containers[0].SecurityContext, privileged, spcT)
			}
		}, 3*time.Minute, 2*time.Second).Should(Succeed())

		for _, pod := range cniPods {
			By("checking the runtime process and installed files on node " + pod.Spec.NodeName)
			Expect(pod.Status.ContainerStatuses).To(HaveLen(1))
			status := pod.Status.ContainerStatuses[0]
			Expect(status.State.Running).NotTo(BeNil())
			pid := containerPID(ctx, pod.Spec.NodeName, status.ContainerID)

			procStatus := nodeExec(ctx, pod.Spec.NodeName, "cat", "/proc/"+pid+"/status")
			if privileged {
				Expect(procField(procStatus, "CapEff")).NotTo(Equal("0000000000000000"), "privileged install-cni must hold capabilities")
				Expect(procField(procStatus, "Seccomp")).To(Equal("0"), "privileged install-cni runs unconfined")
			} else {
				// Empty permitted and bounding sets too: the process cannot regain
				// capabilities, not even through execve of a file-capability binary.
				for _, set := range []string{"CapEff", "CapPrm", "CapBnd"} {
					Expect(procField(procStatus, set)).To(Equal("0000000000000000"), "install-cni %s must be empty", set)
				}
				Expect(procField(procStatus, "NoNewPrivs")).To(Equal("1"))
				Expect(procField(procStatus, "Seccomp")).To(Equal("2"), "install-cni must run under a seccomp filter")
				Expect(rootMountOptions(nodeExec(ctx, pod.Spec.NodeName, "cat", "/proc/"+pid+"/mounts"))).
					To(HavePrefix("ro,"), "install-cni root filesystem must be read-only")
			}

			// install-cni writes every file via rename on start, so an mtime at
			// or after the container start proves this container wrote them.
			started := status.State.Running.StartedAt.Unix()
			for _, path := range []string{
				"/opt/cni/bin/whereabouts",
				"/etc/cni/net.d/whereabouts.d/whereabouts.conf",
				"/etc/cni/net.d/whereabouts.d/whereabouts.kubeconfig",
				"/etc/cni/net.d/whereabouts.d/nodename",
			} {
				fields := strings.Fields(nodeExec(ctx, pod.Spec.NodeName, "stat", "-c", "%Y %s %A", path))
				Expect(fields).To(HaveLen(3), path)
				mtime, err := strconv.ParseInt(fields[0], 10, 64)
				Expect(err).NotTo(HaveOccurred())
				Expect(mtime).To(BeNumerically(">=", started), "%s was not written by the running install-cni", path)
				Expect(fields[1]).NotTo(Equal("0"), "%s is empty", path)
				if path == "/opt/cni/bin/whereabouts" {
					Expect(fields[2]).To(MatchRegexp(`^-..x`), "%s is not executable", path)
				}
			}
			Expect(nodeExec(ctx, pod.Spec.NodeName, "cat", "/etc/cni/net.d/whereabouts.d/nodename")).
				To(Equal(pod.Spec.NodeName))
		}

		By("allocating a whereabouts IP to a pod on every schedulable node")
		nad := util.MacvlanNetworkWithWhereaboutsIPAMNetwork(networkName, testNamespace, ipRange, []string{}, wbstorage.UnnamedNetwork, true)
		_, err = clientInfo.AddNetAttachDef(nad)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(clientInfo.DelNetAttachDef(nad)).To(Succeed()) })

		for i, node := range nodes.Items {
			if _, controlPlane := node.Labels["node-role.kubernetes.io/control-plane"]; controlPlane {
				continue
			}
			name := fmt.Sprintf("wb-cni-install-%d", i)
			pod := entities.PodObject(name, testNamespace, util.PodTierLabel(name), entities.PodNetworkSelectionElements(networkName))
			pod.Spec.NodeName = node.Name
			pod, err = clientInfo.Client.CoreV1().Pods(testNamespace).Create(ctx, pod, metav1.CreateOptions{})
			Expect(err).NotTo(HaveOccurred())
			pods = append(pods, pod)
		}
		Expect(pods).NotTo(BeEmpty())
		for _, pod := range pods {
			Expect(wbtestclient.WaitForPodReady(ctx, clientInfo.Client, testNamespace, pod.Name, 90*time.Second)).To(Succeed(), pod.Spec.NodeName)
			pod, err = clientInfo.Client.CoreV1().Pods(testNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			ips, err := retrievers.SecondaryIfaceIPValue(pod, "net1")
			Expect(err).NotTo(HaveOccurred())
			Expect(ips).NotTo(BeEmpty())
			Expect(util.InRange(ipRange, ips[0])).To(Succeed())
		}
	})
})

func expectCNISecurityContext(g Gomega, sc *corev1.SecurityContext, privileged, spcT bool) {
	g.Expect(sc).NotTo(BeNil())
	g.Expect(sc.RunAsUser).To(HaveValue(BeZero()))
	if spcT {
		// kind nodes have no SELinux, so the runtime ignores the label: this
		// run can only verify the rendered spec and that the pods still run.
		g.Expect(sc.SELinuxOptions).NotTo(BeNil())
		g.Expect(sc.SELinuxOptions.Type).To(Equal("spc_t"))
	} else {
		g.Expect(sc.SELinuxOptions).To(BeNil())
	}
	if privileged {
		g.Expect(sc.Privileged).To(HaveValue(BeTrue()))
		return
	}
	g.Expect(sc.Privileged).To(HaveValue(BeFalse()))
	g.Expect(sc.AllowPrivilegeEscalation).To(HaveValue(BeFalse()))
	g.Expect(sc.ReadOnlyRootFilesystem).To(HaveValue(BeTrue()))
	g.Expect(sc.Capabilities).NotTo(BeNil())
	g.Expect(sc.Capabilities.Drop).To(ConsistOf(corev1.Capability("ALL")))
	g.Expect(sc.Capabilities.Add).To(BeEmpty())
	g.Expect(sc.SeccompProfile).NotTo(BeNil())
	g.Expect(sc.SeccompProfile.Type).To(Equal(corev1.SeccompProfileTypeRuntimeDefault))
}

func podReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// containerPID returns the host PID of a container on a kind node via crictl.
func containerPID(ctx context.Context, node, containerID string) string {
	_, id, found := strings.Cut(containerID, "://")
	Expect(found).To(BeTrue(), "unexpected container ID %q", containerID)
	var inspect struct {
		Info struct {
			Pid int `json:"pid"`
		} `json:"info"`
	}
	Expect(json.Unmarshal([]byte(nodeExec(ctx, node, "crictl", "inspect", id)), &inspect)).To(Succeed())
	Expect(inspect.Info.Pid).To(BeNumerically(">", 0))
	return strconv.Itoa(inspect.Info.Pid)
}

// nodeExec runs a command inside a kind node container and returns its trimmed stdout.
func nodeExec(ctx context.Context, node string, args ...string) string {
	out, err := exec.CommandContext(ctx, ociBin(), append([]string{"exec", node}, args...)...).Output()
	var stderr []byte
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	Expect(err).NotTo(HaveOccurred(), "%s on %s: %s", strings.Join(args, " "), node, stderr)
	return strings.TrimSpace(string(out))
}

// procField returns the value of a "Key:\tvalue" line of /proc/<pid>/status.
func procField(status, key string) string {
	for _, line := range strings.Split(status, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	Fail("no " + key + " in /proc status")
	return ""
}

// rootMountOptions returns the options of the last "/" mount in /proc/<pid>/mounts.
func rootMountOptions(mounts string) string {
	var opts string
	for _, line := range strings.Split(mounts, "\n") {
		if f := strings.Fields(line); len(f) >= 4 && f[1] == "/" {
			opts = f[3] + ","
		}
	}
	return opts
}
