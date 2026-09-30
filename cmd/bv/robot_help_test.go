package main

import (
	"bytes"
	"strings"
	"testing"
)

// --robot-help is generated from the registries: every registered command
// appears with its description, modifiers are listed separately, and the
// envelope contract is stated once.
func TestWriteRobotHelp_ListsEveryRegisteredCommand(t *testing.T) {
	active := true
	var reg RobotRegistry
	reg.Register(RobotCommand{Name: "alpha", FlagName: "robot-alpha", FlagPtr: &active, Description: "Alpha output", Handler: func(RobotContext) error { return nil }})
	reg.Register(RobotCommand{Name: "beta", FlagName: "robot-beta", FlagPtr: &active, Description: "Beta output", Handler: func(RobotContext) error { return nil }})
	reg.Register(RobotCommand{Name: "by-thing", FlagName: "robot-by-thing", FlagPtr: &active, Description: "Group alpha by thing", IsModifier: true, Handler: func(RobotContext) error { return nil }})

	var out bytes.Buffer
	if err := writeRobotHelpFromRegistries(&out, &reg); err != nil {
		t.Fatalf("writeRobotHelpFromRegistries: %v", err)
	}
	text := out.String()
	for _, want := range []string{"--robot-alpha", "Alpha output", "--robot-beta", "Beta output", "source_path", "scope.unsupported"} {
		if !strings.Contains(text, want) {
			t.Errorf("help is missing %q:\n%s", want, text)
		}
	}
	cmdIdx := strings.Index(text, "All robot commands:")
	modIdx := strings.Index(text, "Modifiers (combine with a command above):")
	thingIdx := strings.Index(text, "--robot-by-thing")
	if cmdIdx < 0 || modIdx < 0 || thingIdx < 0 {
		t.Fatalf("expected command and modifier sections:\n%s", text)
	}
	if !(cmdIdx < modIdx && modIdx < thingIdx) {
		t.Errorf("modifier must be listed under the modifiers heading, not among commands:\n%s", text)
	}
	if strings.Count(text, "--robot-alpha") != 1 {
		t.Errorf("each command must be listed once:\n%s", text)
	}
}

// The real registries are populated in main; the package-level writer must
// still list the core commands an agent starts with.
func TestWriteRobotHelp_DefaultRegistriesOrIntro(t *testing.T) {
	var out bytes.Buffer
	if err := writeRobotHelp(&out); err != nil {
		t.Fatalf("writeRobotHelp: %v", err)
	}
	for _, want := range []string{"--robot-triage", "--robot-next", "--robot-capabilities", "Every payload carries"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help is missing %q", want)
		}
	}
}
