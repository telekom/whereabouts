// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
//
// SPDX-License-Identifier: Apache-2.0

package whereabouts_e2e

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
	wbtestclient "github.com/telekom/whereabouts/e2e/client"
	"github.com/telekom/whereabouts/e2e/util"
)

// The webhook server pins TLS 1.2+ with ECDHE AEAD suites by default. CI
// re-runs this spec after `helm upgrade --set operator.webhookTLS.goDefaults=true`
// (E2E_WEBHOOK_TLS_GO_DEFAULTS=true) to cover the opt-out.
var _ = Describe("Webhook TLS", func() {
	const (
		operatorNamespace = "kube-system"
		operatorSelector  = "control-plane=controller-manager"
		webhookPort       = 9443
		goDefaultsFlag    = "--webhook-tls-go-defaults=true"
	)

	var (
		clientInfo *wbtestclient.ClientInfo
		goDefaults bool
		addr       string
	)

	BeforeEach(func() {
		ctx := context.Background()
		config, err := util.ClusterConfig()
		Expect(err).NotTo(HaveOccurred())
		clientInfo, err = wbtestclient.NewClientInfo(config)
		Expect(err).NotTo(HaveOccurred())
		goDefaults = os.Getenv("E2E_WEBHOOK_TLS_GO_DEFAULTS") == "true"

		By(fmt.Sprintf("waiting for ready operator pods with %s=%t", goDefaultsFlag, goDefaults))
		var operatorPod string
		Eventually(func(g Gomega) {
			list, err := clientInfo.Client.CoreV1().Pods(operatorNamespace).List(ctx, metav1.ListOptions{LabelSelector: operatorSelector})
			g.Expect(err).NotTo(HaveOccurred())
			operatorPod = ""
			for _, pod := range list.Items {
				g.Expect(pod.DeletionTimestamp).To(BeNil(), "old operator pod %s still terminating", pod.Name)
				g.Expect(slices.ContainsFunc(pod.Status.Conditions, func(c corev1.PodCondition) bool {
					return c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue
				})).To(BeTrue(), "operator pod %s not ready", pod.Name)
				g.Expect(slices.ContainsFunc(pod.Spec.Containers, func(c corev1.Container) bool {
					return slices.Contains(c.Args, goDefaultsFlag)
				})).To(Equal(goDefaults), "operator pod %s args", pod.Name)
				operatorPod = pod.Name
			}
			g.Expect(operatorPod).NotTo(BeEmpty())
		}, 3*time.Minute, 2*time.Second).Should(Succeed())

		By("port-forwarding to the webhook server of " + operatorPod)
		addr = portForward(operatorNamespace, "pod/"+operatorPod, webhookPort)
	})

	handshake := func(cfg *tls.Config) (tls.ConnectionState, error) {
		// Only protocol negotiation is under test; the API server call below
		// covers certificate verification.
		cfg.InsecureSkipVerify = true
		dialer := &tls.Dialer{Config: cfg}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return tls.ConnectionState{}, err
		}
		defer conn.Close()
		return conn.(*tls.Conn).ConnectionState(), nil
	}

	pinnedSuites := []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
	}
	ecdheCBC := []uint16{
		tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_CBC_SHA,
		tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_CBC_SHA,
	}

	It("negotiates TLS 1.3 and TLS 1.2 with each ECDHE AEAD suite", func() {
		state, err := handshake(&tls.Config{MinVersion: tls.VersionTLS13})
		Expect(err).NotTo(HaveOccurred(), "TLS 1.3")
		Expect(state.Version).To(Equal(uint16(tls.VersionTLS13)))

		state, err = handshake(&tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: pinnedSuites})
		Expect(err).NotTo(HaveOccurred(), "TLS 1.2 ECDHE AEAD")
		Expect(state.Version).To(Equal(uint16(tls.VersionTLS12)))
		// A suite is usable only with its certificate key type.
		keyType := "_RSA_"
		if state.PeerCertificates[0].PublicKeyAlgorithm == x509.ECDSA {
			keyType = "_ECDSA_"
		}

		for _, suite := range pinnedSuites {
			name := tls.CipherSuiteName(suite)
			state, err := handshake(&tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{suite}})
			if !strings.Contains(name, keyType) {
				Expect(err).To(HaveOccurred(), "%s needs a different certificate key type", name)
				continue
			}
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(state.Version).To(Equal(uint16(tls.VersionTLS12)), name)
			Expect(state.CipherSuite).To(Equal(suite), name)
		}
	})

	It("rejects TLS 1.1 and TLS 1.2 RSA key exchange in both modes", func() {
		_, err := handshake(&tls.Config{MinVersion: tls.VersionTLS11, MaxVersion: tls.VersionTLS11})
		Expect(err).To(MatchError(MatchRegexp("protocol version")), "TLS 1.1 must be rejected")

		_, err = handshake(&tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_128_GCM_SHA256, tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
		}})
		Expect(err).To(MatchError(ContainSubstring("handshake failure")), "TLS 1.2 RSA key exchange must be rejected")
	})

	It("rejects non-AEAD TLS 1.2 suites unless the Go-defaults opt-out is set", func() {
		state, err := handshake(&tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: ecdheCBC})
		if goDefaults {
			Expect(err).NotTo(HaveOccurred(), "Go defaults accept ECDHE-CBC")
			Expect(ecdheCBC).To(ContainElement(state.CipherSuite))
			return
		}
		Expect(err).To(MatchError(ContainSubstring("handshake failure")), "ECDHE-CBC must be rejected")
	})

	It("still serves the API server's admission calls", func() {
		// failurePolicy is Ignore, so a failed API server handshake would
		// silently admit this invalid pool instead of rejecting it.
		pool := &v1alpha1.IPPool{
			ObjectMeta: metav1.ObjectMeta{Name: "wb-webhook-tls-probe", Namespace: ipPoolNamespace},
			Spec:       v1alpha1.IPPoolSpec{Range: "not-a-cidr", Allocations: map[string]v1alpha1.IPAllocation{}},
		}
		pools := clientInfo.WbClient.WhereaboutsV1alpha1().IPPools(ipPoolNamespace)
		_, err := pools.Create(context.Background(), pool, metav1.CreateOptions{})
		if err == nil {
			_ = pools.Delete(context.Background(), pool.Name, metav1.DeleteOptions{})
		}
		Expect(err).To(MatchError(ContainSubstring("invalid spec.range")))
	})
})

var forwardingFrom = regexp.MustCompile(`^Forwarding from (127\.0\.0\.1:\d+) ->`)

// portForward runs `kubectl port-forward` to target until the spec ends and
// returns the local address.
func portForward(namespace, target string, port int) string {
	cmd := exec.Command("kubectl", "port-forward", "-n", namespace, target, fmt.Sprintf(":%d", port))
	cmd.Stderr = GinkgoWriter
	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	Expect(cmd.Start()).To(Succeed())
	DeferCleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	addr := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if m := forwardingFrom.FindStringSubmatch(scanner.Text()); m != nil {
				addr <- m[1]
				break
			}
		}
		for scanner.Scan() { // keep draining so kubectl never blocks on stdout
		}
	}()
	var local string
	Eventually(addr, 30*time.Second).Should(Receive(&local), "kubectl port-forward did not start")
	return local
}
