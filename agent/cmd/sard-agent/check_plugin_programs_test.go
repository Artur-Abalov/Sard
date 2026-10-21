// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package main

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/Artur-Abalov/sard/agent/plugins"
	"github.com/Artur-Abalov/sard/agent/plugins/sdk"
)

// Rule "Проверка программ плагина принадлежит плагину" (R2c-3, В1): any plugin
// may check its programs on the host; the agent knows no program of any.

// stubPlugin is a plugin of the tests that does nothing but be registered.
type stubPlugin struct{ name string }

func (p stubPlugin) Name() string                                      { return p.name }
func (stubPlugin) Version() string                                     { return version }
func (stubPlugin) ConfigSchema() []byte                                { return []byte(`{"type":"object"}`) }
func (stubPlugin) Prepare(context.Context, sdk.Host, sdk.Config) error { return nil }
func (stubPlugin) Dump(context.Context, sdk.Host, sdk.Config) (sdk.Dump, error) {
	return sdk.Dump{}, nil
}
func (stubPlugin) Stream(context.Context, sdk.Host, sdk.Config, sdk.Dump, io.Writer) error {
	return nil
}

// programsPlugin is a plugin of the tests that checks its programs.
type programsPlugin struct {
	stubPlugin
	check func(sdk.ProgramHost) sdk.ProgramsCheck
}

func (p programsPlugin) CheckPrograms(h sdk.ProgramHost) sdk.ProgramsCheck { return p.check(h) }

// withPlugin adds a plugin to the built-in ones of the host.
func (h *setupHost) withPlugin(added ...sdk.Plugin) {
	h.t.Helper()
	r, err := sdk.NewRegistry(append(plugins.Builtin(version), added...)...)
	ok(h.t, err)
	h.deps.plugins = r
}

func TestThePluginsOwnWordsAreTheLineOfItsPrograms(t *testing.T) {
	for status, want := range map[sdk.ProgramsStatus]string{
		sdk.ProgramsOK: "OK", sdk.ProgramsSkip: "SKIP", sdk.ProgramsWarn: "WARN", sdk.ProgramsError: "ERROR",
	} {
		h := fullHost(t)
		h.withPlugin(programsPlugin{stubPlugin: stubPlugin{"zeta"}, check: func(sdk.ProgramHost) sdk.ProgramsCheck {
			return sdk.ProgramsCheck{Status: status, Message: "zeta-tool is old", Hint: "upgrade zeta-tool"}
		}})
		lines, _, _ := h.checkLines()
		l := line(t, lines, "plugin.programs", "zeta")
		if l.status != want || l.message != "zeta-tool is old" || l.hint != "upgrade zeta-tool" {
			t.Errorf("%s: %+v", want, l)
		}
	}
}

func TestAPluginAnswerWithoutAStatusIsAnErrorOfTheCheck(t *testing.T) {
	h := fullHost(t)
	h.withPlugin(programsPlugin{stubPlugin: stubPlugin{"zeta"}, check: func(sdk.ProgramHost) sdk.ProgramsCheck { return sdk.ProgramsCheck{Message: "?"} }})
	lines, _, _ := h.checkLines()
	if l := line(t, lines, "plugin.programs", "zeta"); l.status != "ERROR" || !strings.Contains(l.message, "no status") {
		t.Errorf("%+v", l)
	}
}

func TestAPluginRunsItsProgramsAsTheServiceUserInTheDirectoriesOfThePath(t *testing.T) {
	h := fullHost(t)
	h.deps.pathEnv = ":/opt/z/bin::/usr/bin"
	h.progs.answer("/opt/z/bin/ztool --version", "ztool 3\n")
	var path []string
	var out string
	var err error
	h.withPlugin(programsPlugin{stubPlugin: stubPlugin{"zeta"}, check: func(ph sdk.ProgramHost) sdk.ProgramsCheck {
		path = ph.Path()
		out, err = ph.Run("/opt/z/bin/ztool", "--version")
		return sdk.ProgramsCheck{Status: sdk.ProgramsOK, Message: strings.TrimSpace(out)}
	}})
	lines, _, _ := h.checkLines()
	if got := strings.Join(path, "|"); got != "/opt/z/bin|/usr/bin" {
		t.Errorf("path %q: the empty entries stand for the current directory and are dropped", got)
	}
	if l := line(t, lines, "plugin.programs", "zeta"); err != nil || l.message != "ztool 3" {
		t.Errorf("%+v, %v", l, err)
	}
	for _, c := range h.progs.calls {
		if c.key == "/opt/z/bin/ztool --version" && (c.runAs == nil || c.runAs.UID != serviceUID) {
			t.Errorf("ztool ran as %+v", c.runAs)
		}
	}
	if _, err := h.deps.plugins.Get("zeta"); !err {
		t.Fatal("zeta is not registered")
	}
}

func TestAProgramThatDoesNotStartIsAnErrorToThePlugin(t *testing.T) {
	h := fullHost(t)
	var err error
	h.withPlugin(programsPlugin{stubPlugin: stubPlugin{"zeta"}, check: func(ph sdk.ProgramHost) sdk.ProgramsCheck {
		_, err = ph.Run("/opt/z/bin/nothing", "--version")
		return sdk.ProgramsCheck{Status: sdk.ProgramsOK, Message: "m"}
	}})
	h.checkLines()
	if err == nil {
		t.Error("a program that is not there started")
	}
}
