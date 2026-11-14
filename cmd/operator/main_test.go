// Copyright 2025 Deutsche Telekom
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestLoggerOptions(t *testing.T) {
	for _, tc := range []struct {
		level, format string
		wantLevel     zapcore.Level
		wantDev       bool
		wantErr       bool
	}{
		{level: "info", wantLevel: zapcore.InfoLevel},
		{level: "debug", wantLevel: zapcore.DebugLevel, wantDev: true},
		{level: "warn", wantLevel: zapcore.WarnLevel},
		{level: "error", wantLevel: zapcore.ErrorLevel},
		{level: "bogus", wantLevel: zapcore.InfoLevel},
		{level: "debug", format: "json", wantLevel: zapcore.DebugLevel},
		{level: "info", format: "console", wantLevel: zapcore.InfoLevel, wantDev: true},
		{level: "error", format: "json", wantLevel: zapcore.ErrorLevel},
		{level: "info", format: "text", wantErr: true},
	} {
		t.Run(tc.level+"/"+tc.format, func(t *testing.T) {
			opts, err := loggerOptions(tc.level, tc.format)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if opts.Level != tc.wantLevel || opts.Development != tc.wantDev {
				t.Fatalf("got level %v development %v, want %v %v", opts.Level, opts.Development, tc.wantLevel, tc.wantDev)
			}
		})
	}
}

func TestSetupLoggerRejectsInvalidFormat(t *testing.T) {
	cmd := newRootCommand()
	if err := cmd.PersistentFlags().Parse([]string{"--log-format=yaml"}); err != nil {
		t.Fatal(err)
	}
	if err := setupLogger(cmd); err == nil {
		t.Fatal("expected an error for --log-format=yaml")
	}
}
