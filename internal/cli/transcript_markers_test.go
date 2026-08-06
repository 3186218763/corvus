package cli

import (
	"reflect"
	"testing"
)

func TestCurrentTranscriptMarkers(t *testing.T) {
	user := transcriptSource{kind: transcriptSourceUser, raw: "u"}
	md := func(s string) transcriptSource { return transcriptSource{kind: transcriptSourceMarkdown, raw: s} }
	bundle := transcriptSource{kind: transcriptSourceReplayBundle}
	tool := transcriptSource{kind: transcriptSourceToolCard}
	fixed := transcriptSource{kind: transcriptSourceFixed}

	cases := []struct {
		name    string
		sources []transcriptSource
		want    []transcriptMarker
	}{
		{"empty", nil, nil},
		{"single user", []transcriptSource{user}, []transcriptMarker{markerUserCurrent}},
		{"one exchange", []transcriptSource{user, md("a1")}, []transcriptMarker{markerUserCurrent, markerAssistantNamed}},
		{"second exchange demotes", []transcriptSource{user, md("a1"), user}, []transcriptMarker{markerNone, markerNone, markerUserCurrent}},
		{"two answers one turn", []transcriptSource{user, md("a1"), tool, md("a2")}, []transcriptMarker{markerUserCurrent, markerNone, markerNone, markerAssistantNamed}},
		{"user between answers", []transcriptSource{user, md("a1"), user, md("a2")}, []transcriptMarker{markerNone, markerNone, markerUserCurrent, markerAssistantNamed}},
		{"bundle alone", []transcriptSource{bundle}, []transcriptMarker{markerAssistantNamed | markerUserCurrent}},
		{"bundle then answer", []transcriptSource{bundle, md("a2")}, []transcriptMarker{markerUserCurrent, markerAssistantNamed}},
		{"bundle then user", []transcriptSource{bundle, user}, []transcriptMarker{markerNone, markerUserCurrent}},
		{"tool cards never marked", []transcriptSource{tool, fixed}, []transcriptMarker{markerNone, markerNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := currentTranscriptMarkers(tc.sources)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("currentTranscriptMarkers(%v) = %v, want %v", tc.sources, got, tc.want)
			}
		})
	}
}
