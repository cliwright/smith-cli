package typeref

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    TypeRef
		wantErr bool
	}{
		{name: "simple", in: "python/astral/lib@1", want: TypeRef{Name: "python/astral/lib", Version: 1}},
		{name: "multi digit version", in: "go/std/lib@25", want: TypeRef{Name: "go/std/lib", Version: 25}},
		{name: "hyphenated slugs", in: "rust/cargo/cli-tool@3", want: TypeRef{Name: "rust/cargo/cli-tool", Version: 3}},
		{name: "no version", in: "python/astral/lib", wantErr: true},
		{name: "empty version", in: "python/astral/lib@", wantErr: true},
		{name: "empty name", in: "@1", wantErr: true},
		{name: "two segments", in: "python/astral@1", wantErr: true},
		{name: "four segments", in: "python/astral/lib/extra@1", wantErr: true},
		{name: "version zero", in: "python/astral/lib@0", wantErr: true},
		{name: "leading zero", in: "python/astral/lib@01", wantErr: true},
		{name: "non integer version", in: "python/astral/lib@1.5", wantErr: true},
		{name: "uppercase name", in: "Python/astral/lib@1", wantErr: true},
		{name: "empty string", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("Parse(%q) = %v, want %v", tt.in, got, tt.want)
			}
			if round := got.String(); round != tt.in {
				t.Errorf("Parse(%q).String() = %q, want the input back", tt.in, round)
			}
		})
	}
}
