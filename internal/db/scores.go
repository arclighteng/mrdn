package db

import (
	"context"
	"fmt"
	"sort"
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
	CompanyName    string    `json:"company_name"`
	MarketScore    float64   `json:"market_score"`
	PolicyScore    float64   `json:"policy_score"`
	InsiderScore   float64   `json:"insider_score"`
	CompositeScore float64   `json:"composite_score"`
	WeightVersion  int       `json:"weight_version"`
	ComputedAt     time.Time `json:"computed_at"`
}

func (s *Store) InsertScore(ctx context.Context, sc Score) error {
	_, err := s.pool.Exec(ctx, `
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
	err := s.pool.QueryRow(ctx, `
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
	rows, err := s.pool.Query(ctx, `
		SELECT id, company_id, market_score, policy_score, insider_score, composite_score, weight_version, computed_at
		FROM scores WHERE company_id = $1 ORDER BY computed_at DESC LIMIT $2
	`, companyID, limit)
	if err != nil {
		return nil, fmt.Errorf("getting score history for company %d: %w", companyID, err)
	}
	defer rows.Close()

	var scores []Score
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
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (c.id)
			c.ticker, c.name, s.market_score, s.policy_score, s.insider_score,
			s.composite_score, s.weight_version, s.computed_at
		FROM scores s
		JOIN companies c ON c.id = s.company_id
		ORDER BY c.id, s.computed_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("getting score rankings: %w", err)
	}
	defer rows.Close()

	var rankings []ScoreRanking
	for rows.Next() {
		var r ScoreRanking
		if err := rows.Scan(&r.Ticker, &r.CompanyName, &r.MarketScore, &r.PolicyScore,
			&r.InsiderScore, &r.CompositeScore, &r.WeightVersion, &r.ComputedAt); err != nil {
			return nil, fmt.Errorf("scanning ranking: %w", err)
		}
		rankings = append(rankings, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rankings: %w", err)
	}

	sort.Slice(rankings, func(i, j int) bool {
		return rankings[i].CompositeScore > rankings[j].CompositeScore
	})

	if len(rankings) > limit {
		rankings = rankings[:limit]
	}
	return rankings, nil
}
