package main

import "testing"

func TestDefaultListenAddress(t *testing.T) {
	tests := []struct {
		name     string
		listen   string
		port     string
		expected string
	}{
		{name: "default", expected: ":8787"},
		{name: "platform port", port: "9000", expected: ":9000"},
		{name: "explicit listen wins", listen: "127.0.0.1:9001", port: "9000", expected: "127.0.0.1:9001"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RELAY_LISTEN", test.listen)
			t.Setenv("PORT", test.port)
			if actual := defaultListenAddress(); actual != test.expected {
				t.Fatalf("defaultListenAddress() = %q, want %q", actual, test.expected)
			}
		})
	}
}
