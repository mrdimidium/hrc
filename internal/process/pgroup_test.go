// SPDX-FileCopyrightText: 2026 Nikolay Govorov
// SPDX-License-Identifier: BSD-3-Clause

package process

import (
	"errors"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestPGroupEnvironment(t *testing.T) {
	g := newTestPGroup(t)
	other := newTestPGroup(t)
	if g.id == other.id || g.id == "" {
		t.Fatal("groups must have distinct, nonempty tracking identities")
	}
	t.Setenv(trackingGroupEnv, "inherited")
	t.Setenv("HRC_PGROUP_INHERITED", "present")
	for _, env := range [][]string{nil, {}, {trackingGroupEnv + "=caller", "VALUE=with spaces", trackingGroupEnv + "=duplicate"}} {
		original := append([]string{}, env...)
		got := g.environment(env)
		if !reflect.DeepEqual(original, append([]string{}, env...)) {
			t.Fatal("environment mutated the caller's slice")
		}
		count := 0
		inherited := false
		for _, entry := range got {
			if strings.HasPrefix(entry, trackingGroupEnv+"=") {
				count++
				if entry != trackingGroupEnv+"="+g.id {
					t.Fatalf("wrong tracking identity: %q", entry)
				}
			}
			inherited = inherited || entry == "HRC_PGROUP_INHERITED=present"
		}
		if count != 1 || inherited != (env == nil) {
			t.Fatalf("environment(%v) = %v", env, got)
		}
	}
}

// Detached descendants outlive their Worker, but not their slot. Closing one
// group must not terminate another slot, even when both run this same binary.
func TestPGroupDetachedDescendants(t *testing.T) {
	groups := []*PGroup{newTestPGroup(t), newTestPGroup(t)}
	children := make([]*descendant, len(groups))
	for i, g := range groups {
		listener := listenDescendant(t)
		p, start, _, output := startGroupChild(t, g, "detached_exit", socketEnv+"="+listener.Socket)
		if _, err := start.Write([]byte{1}); err != nil {
			t.Fatal(err)
		}
		children[i] = listener.Accept()
		if state, err := p.Wait(); err != nil || state.ExitCode() != 17 {
			t.Fatalf("Wait = %v, %v: %s", state, err, output)
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		children[i].Check(true)
	}
	if err := groups[0].Close(); err != nil {
		t.Fatal(err)
	}
	children[0].Check(false)
	children[1].Check(true)
	if err := groups[1].Close(); err != nil {
		t.Fatal(err)
	}
	children[1].Check(false)
}

// An environment-tagged process need not be in a native process group or a
// Windows job object. This exercises the scanner independently of that safety
// net, including on Windows.
func TestPGroupTracksEnvironment(t *testing.T) {
	g := newTestPGroup(t)
	listener := listenDescendant(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, descendantArg)
	cmd.Env = g.environment(append(os.Environ(), socketEnv+"="+listener.Socket))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	child := listener.Accept()
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	child.Check(false)
}

func TestPGroupClose(t *testing.T) {
	g := newTestPGroup(t)
	p, _, _, _ := startGroupChild(t, g, "report")
	var closers sync.WaitGroup
	for range 4 {
		closers.Go(func() {
			if err := g.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	closers.Wait()
	if _, err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Start(Config{}); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Start after Close = %v", err)
	}
	if _, err := (*PGroup)(nil).Start(Config{}); err == nil {
		t.Fatal("started a process without a group")
	}
	var zero PGroup
	if _, err := zero.Start(Config{}); err == nil {
		t.Fatal("started a process in an uninitialized group")
	}
	if err := zero.Close(); err == nil {
		t.Fatal("scanned processes with an empty tracking identity")
	}
}
