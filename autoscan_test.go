package autoscan

import (
	"strings"
	"testing"
)

func TestRewriter(t *testing.T) {
	type Test struct {
		Name     string
		Rewrites []Rewrite
		Input    string
		Expected string
	}

	var testCases = []Test{
		{
			Name:     "One parameter with wildcard",
			Input:    "/mnt/unionfs/Media/Movies/Example Movie/movie.mkv",
			Expected: "/data/Movies/Example Movie/movie.mkv",
			Rewrites: []Rewrite{{
				From: "/mnt/unionfs/Media/",
				To:   "/data/",
			}},
		},
		{
			Name:     "One parameter with glob thingy",
			Input:    "/Media/Movies/test.mkv",
			Expected: "/data/Movies/test.mkv",
			Rewrites: []Rewrite{{
				From: "/Media/(.*)",
				To:   "/data/$1",
			}},
		},
		{
			Name:     "No wildcard",
			Input:    "/Media/whatever",
			Expected: "/whatever",
			Rewrites: []Rewrite{{
				From: "^/Media/",
				To:   "/",
			}},
		},
		{
			Name:     "Unicode (PAS issue #73)",
			Input:    "/media/b33f/saitoh183/private/Videos/FrenchTV/L'échappée/Season 03",
			Expected: "/Videos/FrenchTV/L'échappée/Season 03",
			Rewrites: []Rewrite{{
				From: "/media/b33f/saitoh183/private/",
				To:   "/",
			}},
		},
		{
			Name:     "Returns input when no rules are given",
			Input:    "/mnt/unionfs/test/example.mp4",
			Expected: "/mnt/unionfs/test/example.mp4",
		},
		{
			Name:     "Returns input when rule does not match",
			Input:    "/test/example.mp4",
			Expected: "/test/example.mp4",
			Rewrites: []Rewrite{{
				From: "^/Media/",
				To:   "/mnt/unionfs/Media/",
			}},
		},
		{
			Name:     "Uses second rule if first one does not match",
			Input:    "/test/example.mp4",
			Expected: "/mnt/unionfs/example.mp4",
			Rewrites: []Rewrite{
				{From: "^/Media/", To: "/mnt/unionfs/Media/"},
				{From: "^/test/", To: "/mnt/unionfs/"},
			},
		},
		{
			Name:     "Hotio",
			Input:    "/movies4k/example.mp4",
			Expected: "/mnt/unionfs/movies4k/example.mp4",
			Rewrites: []Rewrite{
				{From: "^/movies/", To: "/mnt/unionfs/movies/"},
				{From: "^/movies4k/", To: "/mnt/unionfs/movies4k/"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			rewriter, err := NewRewriter(tc.Rewrites)

			if err != nil {
				t.Fatal(err)
			}

			result := rewriter(tc.Input)
			if result != tc.Expected {
				t.Errorf("%s does not equal %s", result, tc.Expected)
			}
		})
	}

}

func TestFilterer(t *testing.T) {
	type Test struct {
		Name     string
		Includes []string
		Excludes []string
		Input    string
		Expected bool
	}

	var testCases = []Test{
		{
			Name:     "Allows every path without patterns",
			Input:    "/mnt/media/Movies/example.mkv",
			Expected: true,
		},
		{
			Name:     "Rejects a path that matches an exclude",
			Excludes: []string{`\.srt$`},
			Input:    "/mnt/media/Movies/example.srt",
			Expected: false,
		},
		{
			Name:     "Allows a path that matches no exclude",
			Excludes: []string{`\.srt$`},
			Input:    "/mnt/media/Movies/example.mkv",
			Expected: true,
		},
		{
			Name:     "Allows a path that matches an include",
			Includes: []string{"^/mnt/media/Movies/"},
			Input:    "/mnt/media/Movies/example.mkv",
			Expected: true,
		},
		{
			Name:     "Rejects a path that matches no include",
			Includes: []string{"^/mnt/media/Movies/"},
			Input:    "/mnt/media/TV/example.mkv",
			Expected: false,
		},
		{
			Name:     "Exclude wins over include",
			Includes: []string{"^/mnt/media/Movies/"},
			Excludes: []string{`\.srt$`},
			Input:    "/mnt/media/Movies/example.srt",
			Expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			filterer, err := NewFilterer(tc.Includes, tc.Excludes)
			if err != nil {
				t.Fatal(err)
			}

			if result := filterer(tc.Input); result != tc.Expected {
				t.Errorf("filterer(%q) = %v, want %v", tc.Input, result, tc.Expected)
			}
		})
	}
}

func TestInvalidPatternsReturnErrors(t *testing.T) {
	const invalid = "(unclosed"

	if _, err := NewRewriter([]Rewrite{{From: invalid, To: "/"}}); err == nil || !strings.Contains(err.Error(), invalid) {
		t.Errorf("NewRewriter error = %v, want an error that names %q", err, invalid)
	}

	if _, err := NewFilterer([]string{invalid}, nil); err == nil || !strings.Contains(err.Error(), "include: "+invalid) {
		t.Errorf("NewFilterer include error = %v, want an error that names include %q", err, invalid)
	}

	if _, err := NewFilterer(nil, []string{invalid}); err == nil || !strings.Contains(err.Error(), "exclude: "+invalid) {
		t.Errorf("NewFilterer exclude error = %v, want an error that names exclude %q", err, invalid)
	}
}
