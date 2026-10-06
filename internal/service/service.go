// Package service installs lg daemon run as a per-user systemd unit or launchd agent.
package service

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// Unit is how a service runs lg daemon run.
type Unit struct {
	// Name is the systemd unit's name without .service, and the end of the launchd label.
	Name string
	// Exe is lg's absolute path.
	Exe string
	// Env is the environment baked into the unit.
	Env map[string]string
	// Log is the launchd agent's stderr.
	Log string
}

// Label is the launchd label of the service named name.
func Label(name string) string { return "com.github.rosenhouse." + name }

// RenderSystemd gives u as a systemd user unit.
func RenderSystemd(u Unit) ([]byte, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	// systemd refuses these in an executable's name, however quoted.
	if strings.ContainsAny(u.Exe, `"'\`) {
		return nil, fmt.Errorf("systemd cannot run a program whose path contains a quote or backslash: %q", u.Exe)
	}
	var b bytes.Buffer
	b.WriteString("[Unit]\nDescription=lg: mirror GitHub Actions runs\n\n[Service]\n")
	fmt.Fprintf(&b, "ExecStart=%s daemon run\n", systemdQuote(u.Exe))
	for _, k := range slices.Sorted(maps.Keys(u.Env)) {
		fmt.Fprintf(&b, "Environment=%s\n", systemdQuote(k+"="+u.Env[k]))
	}
	b.WriteString("Restart=on-failure\nRestartSec=30\n\n[Install]\nWantedBy=default.target\n")
	return b.Bytes(), nil
}

// systemdQuote quotes s as one word, with its specifiers and C escapes escaped.
func systemdQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`).Replace(s) + `"`
}

// RenderLaunchd gives u as a launchd agent's property list.
func RenderLaunchd(u Unit) ([]byte, error) {
	if err := u.check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
`)
	fmt.Fprintf(&b, "\t<string>%s</string>\n", xmlText(Label(u.Name)))
	b.WriteString("\t<key>ProgramArguments</key>\n\t<array>\n")
	for _, arg := range []string{u.Exe, "daemon", "run"} {
		fmt.Fprintf(&b, "\t\t<string>%s</string>\n", xmlText(arg))
	}
	b.WriteString("\t</array>\n\t<key>EnvironmentVariables</key>\n\t<dict>\n")
	for _, k := range slices.Sorted(maps.Keys(u.Env)) {
		fmt.Fprintf(&b, "\t\t<key>%s</key>\n\t\t<string>%s</string>\n", xmlText(k), xmlText(u.Env[k]))
	}
	b.WriteString(`	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>StandardErrorPath</key>
`)
	fmt.Fprintf(&b, "\t<string>%s</string>\n</dict>\n</plist>\n", xmlText(u.Log))
	return b.Bytes(), nil
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// check refuses control characters and invalid UTF-8, which neither format can carry.
func (u Unit) check() error {
	values := append([]string{u.Name, u.Exe, u.Log}, slices.Collect(maps.Keys(u.Env))...)
	for _, v := range append(values, slices.Collect(maps.Values(u.Env))...) {
		if strings.ContainsFunc(v, isControl) {
			return fmt.Errorf("a service cannot carry a control character: %q", v)
		}
		if !utf8.ValidString(v) {
			return fmt.Errorf("a service cannot carry invalid UTF-8: %q", v)
		}
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }
