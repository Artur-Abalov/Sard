// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package verifier

import (
	"context"
	"sync"
	"time"

	"github.com/Artur-Abalov/sard/agent/internal/executor"
	"github.com/Artur-Abalov/sard/agent/internal/sandbox"
	agentv1 "github.com/Artur-Abalov/sard/proto/gen/go/sard/agent/v1"
	"google.golang.org/protobuf/proto"
)

// Status is what a verifier reports about its host (V5): the room on its
// work volume, its sandbox runtime and the images of its list it has. Register
// carries it always, a heartbeat only when it changed for the server.
type Status struct {
	WorkDir string
	Disk    Disk
	CLI     sandbox.CLI
	Images  []sandbox.Image
	Clock   executor.Clock
	// Refresh is how often the runtime and its images are asked again.
	Refresh time.Duration

	mu       sync.Mutex
	runtime  sandbox.Runtime
	present  []sandbox.Image
	askedAt  time.Time
	asked    bool
	reported *agentv1.VerifierStatus
}

// Current measures the status now and remembers it as reported.
func (s *Status) Current(ctx context.Context) *agentv1.VerifierStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reported = s.measure(ctx)
	return s.reported
}

// Runtime is the container runtime as last asked, asked again once Refresh
// has passed: the one the sandboxes of the checks start with.
func (s *Status) Runtime(ctx context.Context) sandbox.Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ask(ctx)
	return s.runtime
}

// Changed is the status when it differs from the last reported one, then
// remembered as reported; nil when it does not.
func (s *Status) Changed(ctx context.Context) *agentv1.VerifierStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.measure(ctx)
	if s.reported != nil && same(s.reported, now) {
		return nil
	}
	s.reported = now
	return now
}

func (s *Status) measure(ctx context.Context) *agentv1.VerifierStatus {
	s.ask(ctx)
	st := &agentv1.VerifierStatus{Sandbox: s.runtime.Kind, SandboxVersion: s.runtime.Version}
	if vol, err := s.Disk.Volume(s.WorkDir); err == nil {
		st.WorkFreeBytes = &vol.Free
	}
	for _, img := range s.present {
		st.Images = append(st.Images, &agentv1.SandboxImage{Name: img.Name, Version: img.Version, Digest: img.Digest})
	}
	return st
}

// ask asks the runtime and its images, at most once per Refresh.
func (s *Status) ask(ctx context.Context) {
	now := s.Clock.Now()
	if s.asked && now.Sub(s.askedAt) < s.Refresh {
		return
	}
	s.runtime = sandbox.Detect(ctx, s.CLI)
	s.present = s.runtime.Present(ctx, s.CLI, s.Images)
	s.askedAt, s.asked = now, true
}

// significant is the change of free space the server hears of: 1% of it.
const significant = 100

// same: nothing but the free space changed, and that by less than 1%.
func same(a, b *agentv1.VerifierStatus) bool {
	x, y := proto.CloneOf(a), proto.CloneOf(b)
	x.WorkFreeBytes, y.WorkFreeBytes = nil, nil
	if !proto.Equal(x, y) || (a.WorkFreeBytes == nil) != (b.WorkFreeBytes == nil) {
		return false
	}
	old, now := a.GetWorkFreeBytes(), b.GetWorkFreeBytes()
	return max(old, now)-min(old, now) < old/significant+1
}
