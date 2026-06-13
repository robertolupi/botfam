package fam

import (
	"reflect"
	"testing"
)

func TestParseChannels(t *testing.T) {
	tests := []struct {
		input        string
		fallback     string
		wantChannels []string
		wantPrimary  string
	}{
		{
			input:        "#botfam",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "#botfam,#ccrep",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam", "#ccrep"},
			wantPrimary:  "#botfam",
		},
		{
			input:        " #botfam ,  #ccrep ",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam", "#ccrep"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        ",,",
			fallback:     "#botfam",
			wantChannels: []string{"#botfam"},
			wantPrimary:  "#botfam",
		},
		{
			input:        "",
			fallback:     "#deep-cuts",
			wantChannels: []string{"#deep-cuts"},
			wantPrimary:  "#deep-cuts",
		},
	}

	for _, tt := range tests {
		gotChannels, gotPrimary := ParseChannels(tt.input, tt.fallback)
		if !reflect.DeepEqual(gotChannels, tt.wantChannels) {
			t.Errorf("ParseChannels(%q, %q) gotChannels = %v, want %v", tt.input, tt.fallback, gotChannels, tt.wantChannels)
		}
		if gotPrimary != tt.wantPrimary {
			t.Errorf("ParseChannels(%q, %q) gotPrimary = %q, want %q", tt.input, tt.fallback, gotPrimary, tt.wantPrimary)
		}
	}
}
