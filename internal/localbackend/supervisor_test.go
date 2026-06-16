package localbackend

import (
	"reflect"
	"testing"
)

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{
			name:    "simple",
			command: "/usr/bin/gopass-secret-service",
			want:    []string{"/usr/bin/gopass-secret-service"},
		},
		{
			name:    "quoted argument",
			command: `/custom/backend --flag "value with spaces"`,
			want:    []string{"/custom/backend", "--flag", "value with spaces"},
		},
		{
			name:    "escaped space",
			command: `/path/with\ space/backend --arg one`,
			want:    []string{"/path/with space/backend", "--arg", "one"},
		},
		{
			name:    "empty quoted argument",
			command: `/bin/app ""`,
			want:    []string{"/bin/app", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitCommand(tt.command)
			if err != nil {
				t.Fatalf("SplitCommand returned error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitCommand() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSplitCommandUnterminatedQuote(t *testing.T) {
	if _, err := SplitCommand(`/bin/app "unterminated`); err == nil {
		t.Fatal("expected unterminated quote error")
	}
}

func TestExpandBackendPlaceholders(t *testing.T) {
	got := expandBackendPlaceholders(
		[]string{"backend", "--control-directory=%B", "--runtime=%R"},
		"/run/user/1000/private/backend-1",
		"/run/user/1000",
	)
	want := []string{"backend", "--control-directory=/run/user/1000/private/backend-1", "--runtime=/run/user/1000"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expandBackendPlaceholders() = %#v, want %#v", got, want)
	}
}
