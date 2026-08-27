package idset

import (
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want []string
	}{
		{name: "nil", ids: nil, want: []string{}},
		{name: "empty", ids: []string{}, want: []string{}},
		{name: "trim empty duplicate and preserve order", ids: []string{" beta ", "", "alpha", "beta", "  ", "gamma", "alpha"}, want: []string{"beta", "alpha", "gamma"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Normalize(test.ids)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Normalize(%#v) = %#v, want %#v", test.ids, got, test.want)
			}
		})
	}
}

func TestCanonical(t *testing.T) {
	want := []string{"alpha", "beta", "gamma"}
	got := Canonical([]string{" beta ", "gamma", "alpha", "beta", ""})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Canonical() = %#v, want %#v", got, want)
	}
}
