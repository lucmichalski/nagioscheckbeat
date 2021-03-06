package check

import (
	"errors"
	"github.com/PhaedrusTheGreek/nagioscheckbeat/config"
	"github.com/PhaedrusTheGreek/nagioscheckbeat/nagiosperf"
	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/mgutz/str"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

type NagiosCheck struct {
	duration time.Duration

	name    string
	enabled bool
	cmd     string
	args    string
}

func (nagiosCheck *NagiosCheck) Setup(config *config.NagiosCheckConfig) error {

	if config == nil {
		return errors.New("Invalid/Missing Nagios Check Configuration")
	}

	if config.Name == nil {
		return errors.New("Must Specify a Nagios Check Name")
	}
	nagiosCheck.name = *config.Name

	if config.Cmd == nil {
		return errors.New("Must Specify a Nagios Check Command")
	}
	nagiosCheck.cmd = *config.Cmd

	nagiosCheck.args = ""
	if config.Args != nil {
		nagiosCheck.args = *config.Args
	}

	nagiosCheck.enabled = true
	if config.Enabled != nil && !*config.Enabled {
		nagiosCheck.enabled = false
	}

	nagiosCheck.duration = 60 * time.Second
	if config.Period != 0 {
		nagiosCheck.duration = config.Period
	}

	return nil
}

func (nagiosCheck *NagiosCheck) Run(publish func([]beat.Event)) {

	if !nagiosCheck.enabled {
		logp.Info("Not starting module %s as not enabled.", nagiosCheck.name)
		return
	}

	logp.Info("Starting metric %s with period %v", nagiosCheck.name, nagiosCheck.duration)

	ticker := time.NewTicker(nagiosCheck.duration)
	defer ticker.Stop()

	for range ticker.C {
		events, err := nagiosCheck.Check()
		if err != nil {
			logp.Err("Error On Command: %q: %v", nagiosCheck.name, err)
		}
		publish(events)
	}

}

func (nagiosCheck *NagiosCheck) Check() (events []beat.Event, err error) {

	startTime := time.Now()
	startMs := startTime.UnixNano() / int64(time.Millisecond)

	check_event := beat.Event{
		Timestamp: startTime,
		Fields: common.MapStr{
			"type":       "nagioscheck",
			"name":       nagiosCheck.name,
			"cmd":        nagiosCheck.cmd,
			"args":       nagiosCheck.args,
		},
	}

	logp.Debug("nagioscheck", "Running Command: %q", nagiosCheck.cmd)

	arg_fields := str.ToArgv(nagiosCheck.args)

	cmd := exec.Command(nagiosCheck.cmd, arg_fields...)
	var waitStatus syscall.WaitStatus

	/* Go will return 'err' if the command exits abnormally (non-zero return code).
	Nagios commands always will exit abnormally when a check fails, so from
	a funcational perspective, this doesn't help us.  Instead, if the ProcessState is nil,
	that tells us that the command coulnd't run for some reason, which does help.
	*/

	output, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil {
		return
	}

	waitStatus = cmd.ProcessState.Sys().(syscall.WaitStatus)

	logp.Debug("nagioscheck", "Command Returned: %q, exit code %d", output, waitStatus.ExitStatus())

	parts := strings.Split(string(output), "|")
	check_event.Fields["message"] = parts[0]
	check_event.Fields["status"] = nagiosperf.NiceStatus(waitStatus.ExitStatus())
	check_event.Fields["status_code"] = waitStatus.ExitStatus()
	check_event.Fields["took_ms"] = time.Now().UnixNano()/int64(time.Millisecond) - startMs

	// publish the check result, even if there is no perf data
	events = append(events, check_event)

	if len(parts) > 1 {
		logp.Debug("nagioscheck", "Parsing: %q", parts[1])
		perfs, errors := nagiosperf.ParsePerfString(parts[1])
		if len(errors) > 0 {
			for _, err := range errors {
				logp.Debug("parse_errors", "Parse Error: %v", err)
			}
		}

		logp.Debug("nagioscheck", "Command Returned '%d' Perf Metrics: %v", len(perfs), perfs)

		for _, perf := range perfs {

			metric_event := beat.Event{
				Timestamp: startTime,
				Fields: common.MapStr{
					"type":       "nagiosmetric",
					"name":       nagiosCheck.name,
					"label":      perf.Label,
					"uom":        perf.Uom,
					"value":      perf.Value,
					"min":        perf.Min,
					"max":        perf.Max,
					"warning":    perf.Warning,
					"critical":   perf.Critical,
				},
			}

			events = append(events, metric_event)

		}
	}

	return
}

func (nagiosCheck *NagiosCheck) Cleanup() error {
	return nil
}
