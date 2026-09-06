package main

import (
	"io"
	"strings"
	"testing"
)

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		noTTY   bool
		wantErr bool
	}{
		{name: "no arguments", args: nil},
		{name: "single dash", args: []string{"-no-tty"}, noTTY: true},
		{name: "double dash", args: []string{"--no-tty"}, noTTY: true},
		{name: "unknown flag", args: []string{"--headless"}, wantErr: true},
		{name: "positional argument", args: []string{"install"}, wantErr: true},
		{name: "flag after argument", args: []string{"install", "--no-tty"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			noTTY, err := parseFlags(tc.args, io.Discard)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseFlags(%q) = nil error, want an error", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlags(%q): %v", tc.args, err)
			}
			if noTTY != tc.noTTY {
				t.Errorf("parseFlags(%q) noTTY = %v, want %v", tc.args, noTTY, tc.noTTY)
			}
		})
	}
}

func TestParseFlagsReportsTheBadArgument(t *testing.T) {
	var out strings.Builder
	if _, err := parseFlags([]string{"--headles"}, &out); err == nil {
		t.Fatal("parseFlags accepted a misspelled flag")
	}
	if !strings.Contains(out.String(), "no-tty") {
		t.Errorf("usage does not name the real flag:\n%s", out.String())
	}
}
