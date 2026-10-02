package logger

import (
	"testing"

	"go.uber.org/zap"
)

func TestLoggerCreationAndLevels(t *testing.T) {
	levels := []string{"debug", "info", "warn", "error"}
	for _, lvl := range levels {
		z, err := New(lvl, "console")
		if err != nil {
			t.Fatalf("failed to create logger for level %s: %v", lvl, err)
		}
		if z == nil {
			t.Fatalf("expected non-nil logger")
		}

		l, err := NewLogger(lvl, "console")
		if err != nil {
			t.Fatalf("failed to create Logger interface for level %s: %v", lvl, err)
		}
		if l == nil {
			t.Fatalf("expected non-nil Logger interface")
		}
	}

	// JSON format
	zJSON, err := New("info", "json")
	if err != nil {
		t.Fatalf("failed to create JSON logger: %v", err)
	}
	if zJSON == nil {
		t.Fatalf("expected non-nil JSON logger")
	}

	// Invalid level
	_, err = New("invalid-level", "console")
	if err == nil {
		t.Errorf("expected error for invalid level")
	}
}

func TestLoggerEnvAndAdapters(t *testing.T) {
	t.Setenv("HERD_LOG_LEVEL", "debug")
	t.Setenv("HERD_LOG_FORMAT", "console")

	z, err := New("", "")
	if err != nil {
		t.Fatalf("New() with env failed: %v", err)
	}

	// Contextual helpers
	nodeLog := WithNode(z, "node-alpha")
	peerLog := WithPeer(nodeLog, "node-beta")
	compLog := WithComponent(peerLog, "gossip")

	if compLog == nil {
		t.Fatalf("expected annotated logger")
	}

	// StdLogger adapter for memberlist
	stdLog := ZapToStdLogger(z)
	if stdLog == nil {
		t.Fatalf("expected valid std logger")
	}
	stdLog.Println("test standard log message")

	// Logf adapter for Tailcat
	sugar := z.Sugar()
	logf := ZapToLogf(sugar)
	if logf == nil {
		t.Fatalf("expected valid Logf adapter")
	}
	logf("test tailcat logf message: %s", "ok")
}

func TestAbstractLoggerMethods(t *testing.T) {
	l, err := NewLogger("debug", "console")
	if err != nil {
		t.Fatalf("NewLogger error: %v", err)
	}

	// Test structured log calls
	l.Debug("debug message", zap.String("k1", "v1"))
	l.Info("info message", zap.String("k2", "v2"))
	l.Warn("warn message", zap.Int("code", 404))
	l.Error("error message", zap.String("err", "something broke"))

	// Test printf-style formatted calls
	l.Debugf("debug: %s", "detail")
	l.Infof("info: %d items", 42)
	l.Warnf("warn: latency %dms", 120)
	l.Errorf("error: code %d", 500)
	l.Printf("printf standard log: %s", "hello")

	// Test contextual annotations
	subLog := l.WithNode("node-1").WithComponent("mailbox").WithPeer("node-2").Named("sub")
	subLog.Info("annotated event")

	// Test adapters
	if subLog.Desugar() == nil || subLog.Sugar() == nil {
		t.Errorf("expected non-nil desugar/sugar")
	}
	std := subLog.ToStdLogger()
	if std == nil {
		t.Errorf("expected non-nil std logger")
	}
	std.Println("std log via abstract logger")

	logf := subLog.Logf()
	if logf == nil {
		t.Errorf("expected non-nil Logf")
	}
	logf("logf message %d", 100)

	// Test NewNop
	nop := NewNop()
	nop.Info("should discard", zap.String("noop", "true"))
	nop.Infof("should discard %s", "formatted")
}
