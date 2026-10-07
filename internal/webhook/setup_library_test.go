// SPDX-FileCopyrightText: 2026 Deutsche Telekom AG
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"context"
	"testing"
)

func TestLibraryRegistrationGracefulCancellation(t *testing.T) {
	mgr, server := setupFixture(t, true)
	check, err := SetupWithManager(mgr, make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.runnable.Start(ctx); err != nil {
		t.Fatalf("shutdown before certificate readiness must not fail manager startup: %v", err)
	}
	if check(nil) == nil || len(server.paths) != 0 {
		t.Fatal("cancellation must not register handlers or report readiness")
	}
}
