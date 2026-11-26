package route

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/inkly/recasaos-wake-on-lan/service"
)

// The card is what the dashboard draws on its home screen (card format v1): the bounds below
// are the format's, and the dashboard refuses the whole card when one is passed.
const (
	cardMaxRows  = 8
	cardMaxLabel = 40
)

type cardAction struct {
	Label  string            `json:"label"`
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Body   map[string]string `json:"body,omitempty"`
}

type cardRow struct {
	Label  string      `json:"label"`
	Value  string      `json:"value,omitempty"`
	Action *cardAction `json:"action,omitempty"`
}

type cardStatus struct {
	Level string `json:"level"`
	Text  string `json:"text"`
}

type cardLink struct {
	Label string `json:"label"`
	Path  string `json:"path"`
}

type card struct {
	Schema  int        `json:"schema"`
	Title   string     `json:"title"`
	Icon    string     `json:"icon"`
	Status  cardStatus `json:"status"`
	Rows    []cardRow  `json:"rows"`
	Link    cardLink   `json:"link"`
	Refresh int        `json:"refresh"`
}

// cut keeps a text within n characters, with an ellipsis where it was cut.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// buildCard is the card of a list of devices: one row for each, the first seven by name when
// there are more than eight, then a row that says how many were left out.
func buildCard(devices []service.Device) card {
	list := append([]service.Device(nil), devices...)
	sort.SliceStable(list, func(i, j int) bool {
		a, b := strings.ToLower(list[i].Name), strings.ToLower(list[j].Name)
		if a != b {
			return a < b
		}
		return list[i].Name < list[j].Name
	})

	c := card{
		Schema:  1,
		Title:   "Wake-on-LAN",
		Icon:    "network-outline",
		Status:  cardStatus{Level: "info", Text: fmt.Sprintf("%d devices", len(list))},
		Rows:    []cardRow{},
		Link:    cardLink{Label: "Open", Path: "/"},
		Refresh: 60,
	}
	if len(list) == 1 {
		c.Status.Text = "1 device"
	}

	shown := list
	if len(list) > cardMaxRows {
		shown = list[:cardMaxRows-1]
	}
	for _, d := range shown {
		c.Rows = append(c.Rows, cardRow{
			Label:  cut(d.Name, cardMaxLabel),
			Value:  d.MAC,
			Action: &cardAction{Label: "Wake", Method: "POST", Path: "/wake", Body: map[string]string{"mac": d.MAC}},
		})
	}
	if len(list) > cardMaxRows {
		c.Rows = append(c.Rows, cardRow{Label: fmt.Sprintf("…and %d more", len(list)-len(shown))})
	}
	if len(list) == 0 {
		c.Rows = append(c.Rows, cardRow{Label: "No device yet", Value: "Add one from the page"})
	}
	return c
}
