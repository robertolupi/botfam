package fam

import (
	"reflect"
	"testing"
)

func TestParseChannels(t *testing.T) {
	tests := []struct {
		input        string
		wantChannels []string
		wantPrimary  string
	}{
		{
			input:        "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "#botfam,#ccrep",
			wantChannels: []string{"#botfam", "#ccrep"},
			wantPrimary:  "#botfam",
		},
		{
			input:        " #botfam ,  #ccrep ",
			wantChannels: []string{"#botfam", "#ccrep"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        ",,",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
	}

	for _, tt := range tests {
		gotChannels, gotPrimary := ParseChannels(tt.input)
		if !reflect.DeepEqual(gotChannels, tt.wantChannels) {
			t.Errorf("ParseChannels(%q) gotChannels = %v, want %v", tt.input, gotChannels, tt.wantChannels)
		}
		if gotPrimary != tt.wantPrimary {
			t.Errorf("ParseChannels(%q) gotPrimary = %q, want %q", tt.input, gotPrimary, tt.wantPrimary)
		}
	}
}
