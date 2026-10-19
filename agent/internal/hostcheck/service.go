// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package hostcheck

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ServiceState is what `systemctl show sard-agent.service` says.
type ServiceState struct {
	ActiveState, SubState string
	Restarts              int
	// Since is when the service became active; zero if it is not.
	Since time.Time
}

// showTimeLayout is the timestamp systemctl show prints (TZ=UTC).
const showTimeLayout = "Mon 2006-01-02 15:04:05 MST"

// showSetters read the fields of systemctl show that are used.
var showSetters = map[string]func(s *ServiceState, value string) error{
	"ActiveState": func(s *ServiceState, v string) error { s.ActiveState = v; return nil },
	"SubState":    func(s *ServiceState, v string) error { s.SubState = v; return nil },
	"NRestarts": func(s *ServiceState, v string) (err error) {
		s.Restarts, err = strconv.Atoi(v)
		return err
	},
	"ActiveEnterTimestamp": func(s *ServiceState, v string) (err error) {
		if v != "" {
			s.Since, err = time.Parse(showTimeLayout, v)
		}
		return err
	},
}

// ParseShow reads the answer of systemctl show: lines of Key=value.
func ParseShow(output string) (ServiceState, error) {
	var s ServiceState
	for line := range strings.Lines(output) {
		key, value, _ := strings.Cut(strings.TrimSpace(line), "=")
		set, known := showSetters[key]
		if !known {
			continue
		}
		if err := set(&s, value); err != nil {
			return ServiceState{}, fmt.Errorf("systemctl show: %s=%q: %w", key, value, err)
		}
	}
	return s, nil
}

// ServiceRunningLine is service.running (Ч21).
func ServiceRunningLine(s ServiceState, now time.Time) Line {
	line := Line{Check: CheckServiceRunning, Status: OK}
	switch {
	case s.ActiveState != "active":
		line.Status = Error
		line.Message = fmt.Sprintf("the service is %s (%s)", s.ActiveState, s.SubState)
		line.Hint = "sudo systemctl start sard-agent; why it stopped: journalctl -u sard-agent"
	case s.Restarts > 0:
		line.Status = Warn
		line.Message = fmt.Sprintf("the service is running, but it restarted %d times after failures", s.Restarts)
		line.Hint = "journalctl -u sard-agent"
	default:
		line.Message = fmt.Sprintf("running since %s (%s)", s.Since.UTC().Format("2006-01-02 15:04:05 MST"), FormatAge(now.Sub(s.Since)))
	}
	return line
}

// ServiceStepsLine is service.steps: the steps in the journal of the
// executor; err is why the journal could not be read.
func ServiceStepsLine(steps int, err error) Line {
	switch {
	case err != nil:
		return Line{Check: CheckServiceSteps, Status: Warn, Message: fmt.Sprintf("the journal of the steps cannot be read: %v", err), Hint: "see " + HelpURL(CheckServiceSteps)}
	case steps == 0:
		return Line{Check: CheckServiceSteps, Status: OK, Message: "no steps in progress"}
	}
	return Line{Check: CheckServiceSteps, Status: OK, Message: fmt.Sprintf("%d steps in progress", steps)}
}
