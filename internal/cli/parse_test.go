package cli

import (
	"flag"
	"testing"
)

func TestReorderCLIArgs_socketAfterKVSet(t *testing.T) {
	args := []string{"cluster/motd", "hello", "--socket", "/tmp/herd.sock"}
	got := ReorderCLIArgs(args)
	want := []string{"--socket", "/tmp/herd.sock", "cluster/motd", "hello"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %q want %q (full: %v)", i, got[i], want[i], got)
		}
	}

	fs := flag.NewFlagSet("kv set", flag.ContinueOnError)
	socket := fs.String("socket", "", "")
	if err := ParseFlags(fs, args); err != nil {
		t.Fatal(err)
	}
	if *socket != "/tmp/herd.sock" {
		t.Fatalf("socket = %q", *socket)
	}
	if fs.Args()[0] != "cluster/motd" || fs.Args()[1] != "hello" {
		t.Fatalf("positionals = %v", fs.Args())
	}
}

func TestReorderCLIArgs_inlineEquals(t *testing.T) {
	args := []string{"mykey", "--socket=/tmp/x.sock"}
	got := ReorderCLIArgs(args)
	if got[0] != "--socket=/tmp/x.sock" || got[1] != "mykey" {
		t.Fatalf("unexpected order: %v", got)
	}
}
