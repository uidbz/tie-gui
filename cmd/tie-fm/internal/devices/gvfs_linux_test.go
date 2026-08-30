//go:build linux

package devices

import "testing"

func TestUSBNode(t *testing.T) {
	tests := []struct {
		key     string
		want    string
		wantErr bool
	}{
		{"8-30", "/dev/bus/usb/008/030", false},
		{"8-28", "/dev/bus/usb/008/028", false},
		{"1-1", "/dev/bus/usb/001/001", false},
		{"12-345", "/dev/bus/usb/012/345", false},
		{"008-030", "/dev/bus/usb/008/030", false},
		{"", "", true},
		{"8", "", true},
		{"x-1", "", true},
		{"1-y", "", true},
	}
	for _, tt := range tests {
		got, err := usbNode(tt.key)
		if tt.wantErr {
			if err == nil {
				t.Errorf("usbNode(%q) = %q, want error", tt.key, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("usbNode(%q) unexpected error: %v", tt.key, err)
			continue
		}
		if got != tt.want {
			t.Errorf("usbNode(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestMTPHost(t *testing.T) {
	tests := []struct {
		root    string
		want    string
		wantErr bool
	}{
		{"mtp://SAMSUNG_SAMSUNG_Android_2ab30210670b7ece/", "SAMSUNG_SAMSUNG_Android_2ab30210670b7ece", false},
		{"mtp://[usb:008,030]/", "[usb:008,030]", false},
		{"mtp:///", "", true},
	}
	for _, tt := range tests {
		got, err := mtpHost(tt.root)
		if tt.wantErr {
			if err == nil {
				t.Errorf("mtpHost(%q) = %q, want error", tt.root, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("mtpHost(%q) unexpected error: %v", tt.root, err)
			continue
		}
		if got != tt.want {
			t.Errorf("mtpHost(%q) = %q, want %q", tt.root, got, tt.want)
		}
	}
}
