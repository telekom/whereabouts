// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	whereaboutsv1alpha1 "github.com/telekom/whereabouts/api/whereabouts.cni.cncf.io/v1alpha1"
)

type setupManager struct {
	manager.Manager
	scheme   *runtime.Scheme
	server   ctrlwebhook.Server
	runnable manager.Runnable
	addErr   error
}

func (m *setupManager) Add(r manager.Runnable) error         { m.runnable = r; return m.addErr }
func (m *setupManager) GetScheme() *runtime.Scheme           { return m.scheme }
func (m *setupManager) GetConfig() *rest.Config              { return &rest.Config{} }
func (m *setupManager) GetWebhookServer() ctrlwebhook.Server { return m.server }

type recordingServer struct {
	ctrlwebhook.Server
	paths          []string
	beforeRegister func(string)
}

func (s *recordingServer) Register(path string, handler http.Handler) {
	if s.beforeRegister != nil {
		s.beforeRegister(path)
	}
	s.paths = append(s.paths, path)
	s.Server.Register(path, handler)
}

func setupFixture(t *testing.T, registeredTypes bool) (*setupManager, *recordingServer) {
	t.Helper()
	scheme := runtime.NewScheme()
	if registeredTypes {
		if err := whereaboutsv1alpha1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
	}
	server := &recordingServer{Server: ctrlwebhook.NewServer(ctrlwebhook.Options{Port: -1, CertDir: "unused"})}
	return &setupManager{scheme: scheme, server: server}, server
}

func waitSetup(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("registration runnable did not stop")
		return nil
	}
}

func TestCertificateGatedRegistration(t *testing.T) {
	mgr, server := setupFixture(t, true)
	ready := make(chan struct{})
	check, err := SetupWithManager(mgr, ready)
	if err != nil {
		t.Fatal(err)
	}
	if mgr.runnable.(manager.LeaderElectionRunnable).NeedLeaderElection() {
		t.Fatal("registration must run on every replica")
	}
	if check(nil) == nil {
		t.Fatal("ready before certificate provisioning")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- mgr.runnable.Start(ctx) }()
	registered := make(chan struct{})
	server.beforeRegister = func(path string) {
		if check(nil) == nil {
			t.Errorf("ready before registration of %s completed", path)
		}
		if path == "/validate-whereabouts-cni-cncf-io-v1alpha1-overlappingrangeipreservation" {
			close(registered)
		}
	}
	close(ready)
	select {
	case <-registered:
	case <-time.After(5 * time.Second):
		t.Fatal("webhooks were not registered")
	}
	deadline := time.Now().Add(5 * time.Second)
	for check(nil) != nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if check(nil) != nil {
		t.Fatal("not ready after registration")
	}
	cancel()
	if err := waitSetup(t, result); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/validate-whereabouts-cni-cncf-io-v1alpha1-ippool",
		"/validate-whereabouts-cni-cncf-io-v1alpha1-nodeslicepool",
		"/validate-whereabouts-cni-cncf-io-v1alpha1-overlappingrangeipreservation",
	}
	if !reflect.DeepEqual(server.paths, want) {
		t.Fatalf("registration order: got %v, want %v", server.paths, want)
	}
}

func TestRegistrationFailureStopsSequence(t *testing.T) {
	mgr, server := setupFixture(t, false)
	ready := make(chan struct{})
	check, err := SetupWithManager(mgr, ready)
	if err != nil {
		t.Fatal(err)
	}
	close(ready)
	if err := mgr.runnable.Start(context.Background()); err == nil {
		t.Fatal("missing scheme must fail first registration")
	}
	if len(server.paths) != 0 || check(nil) == nil {
		t.Fatal("failed first callback must prevent registration and readiness")
	}
}

func TestCancellationBeforeCertificateReady(t *testing.T) {
	mgr, server := setupFixture(t, true)
	check, err := SetupWithManager(mgr, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan error, 1)
	go func() { result <- mgr.runnable.Start(ctx) }()
	if err := waitSetup(t, result); err != nil {
		t.Fatalf("shutdown before certificate readiness must not fail manager startup: %v", err)
	}
	if len(server.paths) != 0 || check(nil) == nil {
		t.Fatal("canceled bootstrap registered handlers or became ready")
	}
}

func TestRegistrationRunnableAddFailure(t *testing.T) {
	mgr, _ := setupFixture(t, true)
	mgr.addErr = errors.New("manager stopped")
	if _, err := SetupWithManager(mgr, make(chan struct{})); !errors.Is(err, mgr.addErr) {
		t.Fatalf("lost manager error: %v", err)
	}
}
