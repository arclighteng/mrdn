package db

import (
	"context"
	"fmt"
	"time"
)

type Score struct {
	ID             int       `json:"id"`
	CompanyID      int       `json:"company_id"`
	MarketScore    float64   `json:"market_score"`
	PolicyScore    float64   `json:"policy_score"`
	InsiderScore   float64   `json:"insider_score"`
	CompositeScore float64   `json:"composite_score"`
	WeightVersion  int       `json:"weight_version"`
	ComputedAt     time.Time `json:"computed_at"`
}

type ScoreRanking struct {
	Ticker         string    `json:"ticker"`
	CompanyName    string    `json:"name"`
	Sector         *string   `json:"sector"`
	MarketScore    float64   `json:"market"`
	PolicyScore    float64   `json:"policy"`
	InsiderScore   float64   `json:"insider"`
	CompositeScore float64   `json:"composite"`
	WeightVersion  int       `json:"weight_version"`
	ComputedAt     time.Time `json:"computed_at"`
}

func (s *Store) InsertScore(ctx context.Context, sc Score) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO scores (company_id, market_score, policy_score, insider_score, composite_score, weight_version)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, sc.CompanyID, sc.MarketScore, sc.PolicyScore, sc.InsiderScore, sc.CompositeScore, sc.WeightVersion)
	if err != nil {
		return fmt.Errorf("inserting score for company %d: %w", sc.CompanyID, err)
	}
	return nil
}

func (s *Store) GetLatestScore(ctx context.Context, companyID int) (Score, error) {
	var sc Score
	err := s.db.QueryRow(ctx, `
		SELECT id, company_id, market_score, policy_score, insider_score, composite_score, weight_version, computed_at
		FROM scores WHERE company_id = $1 ORDER BY computed_at DESC LIMIT 1
	`, companyID).Scan(&sc.ID, &sc.CompanyID, &sc.MarketScore, &sc.PolicyScore,
		&sc.InsiderScore, &sc.CompositeScore, &sc.WeightVersion, &sc.ComputedAt)
	if err != nil {
		return Score{}, fmt.Errorf("getting latest score for company %d: %w", companyID, err)
	}
	return sc, nil
}

func (s *Store) GetScoreHistory(ctx context.Context, companyID int, limit int) ([]Score, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, company_id, market_score, policy_score, insider_score, composite_score, weight_version, computed_at
		FROM scores WHERE company_id = $1 ORDER BY computed_at DESC LIMIT $2
	`, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("getting score history for company %d: %w", companyID, err)
	}
	defer rows.Close()

	scores := make([]Score, 0)
	for rows.Next() {
		var sc Score
		if err := rows.Scan(&sc.ID, &sc.CompanyID, &sc.MarketScore, &sc.PolicyScore,
			&sc.InsiderScore, &sc.CompositeScore, &sc.WeightVersion, &sc.ComputedAt); err != nil {
			return nil, fmt.Errorf("scanning score: %w", err)
		}
		scores = append(scores, sc)
	}
	return scores, rows.Err()
}

func (s *Store) GetScoreRankings(ctx context.Context, limit int) ([]ScoreRanking, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (company_id)
				company_id, market_score, policy_score, insider_score,
				composite_score, weight_version, computed_at
			FROM scores
			ORDER BY company_id, computed_at DESC
		)
		SELECT c.ticker, c.name, c.sector, l.market_score, l.policy_score, l.insider_score,
			l.composite_score, l.weight_version, l.computed_at
		FROM latest l
		JOIN companies c ON c.id = l.company_id
		ORDER BY l.composite_score DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("getting score rankings: %w", err)
	}
	defer rows.Close()

	rankings := make([]ScoreRanking, 0)
	for rows.Next() {
		var r ScoreRanking
		if err := rows.Scan(&r.Ticker, &r.CompanyName, &r.Sector, &r.MarketScore, &r.PolicyScore,
			&r.InsiderScore, &r.CompositeScore, &r.WeightVersion, &r.ComputedAt); err != nil {
			return nil, fmt.Errorf("scanning ranking: %w", err)
		}
		rankings = append(rankings, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rankings: %w", err)
	}
	return rankings, nil
}

// ScoreMover captures the change in composite score for a company between its
// most recent score within the given time window and the score immediately
// preceding that window entry.
type ScoreMover struct {
	Ticker        string  `json:"ticker"`
	CompanyName   string  `json:"name"`
	PreviousScore float64 `json:"previous_score"`
	CurrentScore  float64 `json:"composite"`
	Change        float64 `json:"delta"`
	AbsChange     float64 `json:"abs_change"`
}

// GetScoreMovers returns up to limit companies with the largest absolute
// composite score change over the last hours hours. Companies with no
// preceding score are excluded. hours defaults to 24; limit defaults to 20.
func (s *Store) GetScoreMovers(ctx context.Context, hours int, limit int) ([]ScoreMover, error) {
	if hours <= 0 {
		hours = 24
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		WITH recent AS (
			SELECT DISTINCT ON (company_id)
				company_id, composite_score, computed_at
			FROM scores
			WHERE computed_at >= NOW() - make_interval(hours => $1)
			ORDER BY company_id, computed_at DESC
		),
		previous AS (
			SELECT DISTINCT ON (s.company_id)
				s.company_id, s.composite_score
			FROM scores s
			JOIN recent r ON r.company_id = s.company_id
			WHERE s.computed_at < r.computed_at
			ORDER BY s.company_id, s.computed_at DESC
		)
		SELECT c.ticker, c.name,
			p.composite_score AS previous_score,
			r.composite_score AS current_score,
			r.composite_score - p.composite_score AS change,
			ABS(r.composite_score - p.composite_score) AS abs_change
		FROM recent r
		JOIN previous p ON p.company_id = r.company_id
		JOIN companies c ON c.id = r.company_id
		ORDER BY abs_change DESC
		LIMIT $2
	`, hours, limit)
	if err != nil {
		return nil, fmt.Errorf("getting score movers: %w", err)
	}
	defer rows.Close()

	movers := make([]ScoreMover, 0)
	for rows.Next() {
		var m ScoreMover
		if err := rows.Scan(&m.Ticker, &m.CompanyName, &m.PreviousScore,
			&m.CurrentScore, &m.Change, &m.AbsChange); err != nil {
			return nil, fmt.Errorf("scanning score mover: %w", err)
		}
		movers = append(movers, m)
	}
	return movers, rows.Err()
}
