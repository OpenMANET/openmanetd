package device

import "testing"

func TestIsLikelySCODeviceName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "sco token", in: "SCO (CVSD)", want: true},
		{name: "hfp token", in: "BlueALSA HFP Audio Gateway", want: true},
		{name: "hsp token", in: "Headset HSP Device", want: true},
		{name: "handsfree token", in: "Handsfree AG", want: true},
		{name: "bluealsa token", in: "bluealsa:DEV=AA:BB,PROFILE=sco", want: true},
		{name: "a2dp is not sco", in: "bluealsa:DEV=AA:BB,PROFILE=a2dp", want: false},
		{name: "non sco device", in: "bcm2835 Headphones", want: false},
		{name: "empty", in: "", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isLikelySCODeviceName(tt.in); got != tt.want {
				t.Fatalf("isLikelySCODeviceName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestResolveDirectBlueALSA(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		spec     string
		wantOK   bool
		wantName string
		wantErr  bool
	}{
		{
			name:     "explicit bluealsa sco",
			spec:     "bluealsa:DEV=41:42:86:99:1D:61,PROFILE=sco",
			wantOK:   true,
			wantName: "bluealsa:DEV=41:42:86:99:1D:61,PROFILE=sco",
			wantErr:  false,
		},
		{
			name:     "non bluealsa spec",
			spec:     "bcm2835 Headphones",
			wantOK:   false,
			wantName: "",
			wantErr:  false,
		},
		{
			name:     "empty",
			spec:     "",
			wantOK:   false,
			wantName: "",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok, err := resolveDirectBlueALSA(tt.spec, true)
			if ok != tt.wantOK {
				t.Fatalf("resolveDirectBlueALSA(%q) ok = %v, want %v", tt.spec, ok, tt.wantOK)
			}

			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveDirectBlueALSA(%q) err = %v, wantErr %v", tt.spec, err, tt.wantErr)
			}

			if ok && got.Name != tt.wantName {
				t.Fatalf("resolveDirectBlueALSA(%q) name = %q, want %q", tt.spec, got.Name, tt.wantName)
			}
		})
	}
}

func TestDeviceIDFromALSAName(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		id, err := deviceIDFromALSAName("bluealsa:PROFILE=sco")
		if err != nil {
			t.Fatalf("deviceIDFromALSAName() unexpected error: %v", err)
		}

		if id[0] != 'b' {
			t.Fatalf("deviceID first byte = %v, want %v", id[0], byte('b'))
		}
	})

	t.Run("too long", func(t *testing.T) {
		t.Parallel()

		long := make([]byte, 256)
		for i := range long {
			long[i] = 'a'
		}

		if _, err := deviceIDFromALSAName(string(long)); err == nil {
			t.Fatalf("deviceIDFromALSAName() expected error for long name")
		}
	})
}
