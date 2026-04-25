package insights

import (
	"encoding/json"
)

// TimelineEntry is a single point in a finding's chronological proof sequence.
type TimelineEntry struct {
	Date  string         `json:"date"`
	Kind  string         `json:"kind"` // trade, event, cluster, price_move
	Label string         `json:"label"`
	Icon  string         `json:"icon"`
	Meta  map[string]any `json:"meta,omitempty"`
}

// EnrichedFinding wraps a Finding with its derived timeline.
type EnrichedFinding struct {
	Finding
	Timeline []TimelineEntry `json:"timeline"`
}

// BuildTimeline extracts a chronological proof sequence from a Finding's embedded JSON data.
func BuildTimeline(f Finding) []TimelineEntry {
	switch f.Type {
	case "pre_event":
		return buildPreEventTimeline(f)
	case "coordinated", "coordinated_trades":
		return buildCoordinatedTimeline(f)
	case "round_trip":
		return buildRoundTripTimeline(f)
	case "committee_relevant":
		return buildCommitteeTimeline(f)
	case "lone_wolf":
		return buildLoneWolfTimeline(f)
	default:
		return buildFallbackTimeline(f)
	}
}

// eventIcon returns an emoji for a given event type.
func eventIcon(eventType string) string {
	switch eventType {
	case "sec_litigation":
		return "⚖️"
	case "government_contract":
		return "📋"
	case "sanctions":
		return "🚫"
	case "regulatory_action":
		return "📜"
	case "tariff_action":
		return "🏷️"
	default:
		return "📰"
	}
}

// decodeDataMap unmarshals a Finding's Data into a map[string]any.
// Returns nil if Data is empty or not a JSON object.
func decodeDataMap(f Finding) map[string]any {
	if len(f.Data) == 0 {
		return nil
	}
	var d map[string]any
	if err := json.Unmarshal(f.Data, &d); err != nil {
		return nil
	}
	return d
}

// strVal returns a string value from a data map, or "" if missing/wrong type.
func strVal(d map[string]any, key string) string {
	if d == nil {
		return ""
	}
	v, ok := d[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// floatVal returns a float64 from a data map, or 0 if missing/wrong type.
func floatVal(d map[string]any, key string) float64 {
	if d == nil {
		return 0
	}
	v, ok := d[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

// buildPreEventTimeline produces two entries: trade point + event point.
// Data keys (from preevent.go): name, ticker, trade_type, amount, trade_date,
// event_type, event_date, days_gap.
func buildPreEventTimeline(f Finding) []TimelineEntry {
	d := decodeDataMap(f)
	if d == nil {
		return buildFallbackTimeline(f)
	}

	ticker := strVal(d, "ticker")
	name := strVal(d, "name")
	tradeType := strVal(d, "trade_type")
	amount := strVal(d, "amount")
	tradeDate := strVal(d, "trade_date")
	eventType := strVal(d, "event_type")
	eventDate := strVal(d, "event_date")

	tradeLabel := name + " " + tradeType + " " + ticker
	if amount != "" {
		tradeLabel += " ($" + amount + ")"
	}

	eventLabel := prettyEventType(eventType)
	if ticker != "" {
		eventLabel += " — " + ticker
	}

	return []TimelineEntry{
		{
			Date:  tradeDate,
			Kind:  "trade",
			Label: tradeLabel,
			Icon:  "💰",
			Meta: map[string]any{
				"ticker":     ticker,
				"trade_type": tradeType,
				"amount":     amount,
			},
		},
		{
			Date:  eventDate,
			Kind:  "event",
			Label: eventLabel,
			Icon:  eventIcon(eventType),
			Meta: map[string]any{
				"event_type": eventType,
				"ticker":     ticker,
			},
		},
	}
}

// buildCoordinatedTimeline produces one cluster entry.
// Data from coordinated.go is a JSON array of {name, date} objects.
// We extract the ticker from the headline and date from the first element.
func buildCoordinatedTimeline(f Finding) []TimelineEntry {
	// Data is an array, not a map — parse separately.
	type repRow struct {
		Name string `json:"name"`
		Date string `json:"date"`
	}
	var rows []repRow
	if len(f.Data) > 0 {
		_ = json.Unmarshal(f.Data, &rows)
	}

	// Derive a representative date from the Finding timestamp.
	date := ""
	if !f.Timestamp.IsZero() {
		date = f.Timestamp.Format("2006-01-02")
	}
	if len(rows) > 0 && rows[0].Date != "" {
		date = rows[0].Date
	}

	repCount := len(rows)
	label := f.Headline

	meta := map[string]any{
		"rep_count": repCount,
	}
	if repCount > 0 {
		names := make([]string, 0, repCount)
		for _, r := range rows {
			if r.Name != "" {
				names = append(names, r.Name)
			}
		}
		if len(names) > 0 {
			meta["reps"] = names
		}
	}

	return []TimelineEntry{
		{
			Date:  date,
			Kind:  "cluster",
			Label: label,
			Icon:  "🔗",
			Meta:  meta,
		},
	}
}

// buildRoundTripTimeline produces two entries: buy + sell.
// Data keys (from roundtrips.go): name, ticker, hold_days, buy_date, sell_date, amount.
func buildRoundTripTimeline(f Finding) []TimelineEntry {
	d := decodeDataMap(f)
	if d == nil {
		return buildFallbackTimeline(f)
	}

	ticker := strVal(d, "ticker")
	name := strVal(d, "name")
	buyDate := strVal(d, "buy_date")
	sellDate := strVal(d, "sell_date")
	amount := strVal(d, "amount")

	buyLabel := name + " buy " + ticker
	if amount != "" {
		buyLabel += " ($" + amount + ")"
	}
	sellLabel := name + " sell " + ticker

	return []TimelineEntry{
		{
			Date:  buyDate,
			Kind:  "trade",
			Label: buyLabel,
			Icon:  "📈",
			Meta: map[string]any{
				"ticker":     ticker,
				"trade_type": "buy",
				"amount":     amount,
			},
		},
		{
			Date:  sellDate,
			Kind:  "trade",
			Label: sellLabel,
			Icon:  "📉",
			Meta: map[string]any{
				"ticker":     ticker,
				"trade_type": "sell",
			},
		},
	}
}

// buildCommitteeTimeline produces one trade entry with committee context.
// Data keys (from committee.go): name, committee, ticker, sector, amount, date.
func buildCommitteeTimeline(f Finding) []TimelineEntry {
	d := decodeDataMap(f)
	if d == nil {
		return buildFallbackTimeline(f)
	}

	ticker := strVal(d, "ticker")
	name := strVal(d, "name")
	committee := strVal(d, "committee")
	sector := strVal(d, "sector")
	amount := strVal(d, "amount")
	date := strVal(d, "date")

	label := name + " traded " + ticker
	if committee != "" {
		label += " (cmte: " + committee + ")"
	}

	return []TimelineEntry{
		{
			Date:  date,
			Kind:  "trade",
			Label: label,
			Icon:  "🏛️",
			Meta: map[string]any{
				"ticker":    ticker,
				"committee": committee,
				"sector":    sector,
				"amount":    amount,
			},
		},
	}
}

// buildLoneWolfTimeline produces one outsized-trade entry.
// Data keys (from lonewolf.go): name, ticker, trade_type, amount, ratio, date.
func buildLoneWolfTimeline(f Finding) []TimelineEntry {
	d := decodeDataMap(f)
	if d == nil {
		return buildFallbackTimeline(f)
	}

	ticker := strVal(d, "ticker")
	name := strVal(d, "name")
	tradeType := strVal(d, "trade_type")
	amount := strVal(d, "amount")
	ratio := strVal(d, "ratio")
	date := strVal(d, "date")

	label := name + " outsized " + tradeType + " " + ticker
	if ratio != "" {
		label += " (" + ratio + " typical)"
	}

	return []TimelineEntry{
		{
			Date:  date,
			Kind:  "trade",
			Label: label,
			Icon:  "🐺",
			Meta: map[string]any{
				"ticker":     ticker,
				"trade_type": tradeType,
				"amount":     amount,
				"ratio":      ratio,
			},
		},
	}
}

// buildFallbackTimeline produces one generic entry for unknown finding types.
func buildFallbackTimeline(f Finding) []TimelineEntry {
	date := ""
	if !f.Timestamp.IsZero() {
		date = f.Timestamp.Format("2006-01-02")
	}
	return []TimelineEntry{
		{
			Date:  date,
			Kind:  "event",
			Label: f.Headline,
			Icon:  "📰",
		},
	}
}
