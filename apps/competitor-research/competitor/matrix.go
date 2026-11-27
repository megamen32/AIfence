package competitor

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

var defaultMatrixWeights = map[string]float64{
	"search":   10,
	"price":    25,
	"purchase": 25,
	"account":  20,
	"service":  10,
	"ux":       10,
}

type matrixInput struct {
	StudyID string                  `json:"study_id"`
	Rows    []competitorObservation `json:"rows"`
	Weights map[string]float64      `json:"weights,omitempty"`
}

type matrixBlock struct {
	Score  *float64 `json:"score,omitempty"`
	Status string   `json:"status"`
	Rows   int      `json:"rows"`
}

type matrixCompetitor struct {
	Competitor    string                 `json:"competitor"`
	Blocks        map[string]matrixBlock `json:"blocks"`
	WeightedScore *float64               `json:"weighted_score,omitempty"`
	Coverage      float64                `json:"coverage"`
	CoveredWeight float64                `json:"covered_weight"`
	ScoredBlocks  int                    `json:"scored_blocks"`
	UnknownBlocks int                    `json:"unknown_blocks"`
}

type matrixResult struct {
	StudyID     string             `json:"study_id"`
	Weights     map[string]float64 `json:"weights"`
	Competitors []matrixCompetitor `json:"competitors"`
	Limitations []string           `json:"limitations"`
	ScoredRows  int                `json:"scored_rows"`
	UnknownRows int                `json:"unknown_rows"`
}

func registerCompetitorMatrix(a *agentsdk.Agent) {
	t := tool.Typed[matrixInput, matrixResult]("summarize_competitor_matrix").
		Description("Calculate the archive-like six-block competitor matrix from validated observation rows. Uses weights search 10, price 25, purchase 25, account 20, service 10, UX 10 by default; unknown or not_connected evidence is excluded from the denominator and never converted to zero. Price rows may be supplied by an existing price tool; this tool does not collect prices.").
		Execute(func(ctx context.Context, in matrixInput) (matrixResult, error) {
			return buildCompetitorMatrix(in)
		}).Build()
	a.RegisterTool(t, agentsdk.AccessUser)
}

func buildCompetitorMatrix(in matrixInput) (matrixResult, error) {
	studyID := strings.TrimSpace(in.StudyID)
	if studyID == "" {
		return matrixResult{}, errors.New("study_id is required")
	}
	weights := make(map[string]float64, len(defaultMatrixWeights))
	for block, weight := range defaultMatrixWeights {
		weights[block] = weight
	}
	for block, weight := range in.Weights {
		if _, ok := weights[block]; ok && weight >= 0 {
			weights[block] = weight
		}
	}
	rowsByCompetitor := map[string]map[string][]float64{}
	statusByCompetitor := map[string]map[string]string{}
	competitorsSet := map[string]bool{}
	scoredRows, unknownRows := 0, 0
	for _, row := range in.Rows {
		competitor := strings.TrimSpace(row.Competitor)
		block := strings.ToLower(strings.TrimSpace(row.Block))
		if competitor == "" || block == "" {
			continue
		}
		competitorsSet[competitor] = true
		if _, ok := weights[block]; !ok {
			continue
		}
		if statusByCompetitor[competitor] == nil {
			statusByCompetitor[competitor] = map[string]string{}
		}
		status := strings.ToLower(strings.TrimSpace(row.Status))
		if status == "" {
			status = "observed"
		}
		if row.Score == nil || *row.Score < 1 || *row.Score > 5 || isUnknownMatrixStatus(status) {
			unknownRows++
			if statusByCompetitor[competitor][block] == "" {
				statusByCompetitor[competitor][block] = status
			}
			continue
		}
		if rowsByCompetitor[competitor] == nil {
			rowsByCompetitor[competitor] = map[string][]float64{}
		}
		rowsByCompetitor[competitor][block] = append(rowsByCompetitor[competitor][block], *row.Score)
		statusByCompetitor[competitor][block] = "observed"
		scoredRows++
	}
	competitors := make([]string, 0, len(competitorsSet))
	for competitor := range competitorsSet {
		competitors = append(competitors, competitor)
	}
	sort.Strings(competitors)
	result := matrixResult{StudyID: studyID, Weights: weights, ScoredRows: scoredRows, UnknownRows: unknownRows}
	for _, competitor := range competitors {
		summary := matrixCompetitor{Competitor: competitor, Blocks: map[string]matrixBlock{}}
		var weightedSum, availableWeight float64
		for _, block := range []string{"search", "price", "purchase", "account", "service", "ux"} {
			values := rowsByCompetitor[competitor][block]
			status := statusByCompetitor[competitor][block]
			if status == "" {
				status = "missing"
			}
			entry := matrixBlock{Status: status, Rows: len(values)}
			if len(values) > 0 {
				var sum float64
				for _, value := range values {
					sum += value
				}
				average := sum / float64(len(values))
				entry.Score = &average
				weightedSum += average * weights[block]
				availableWeight += weights[block]
				summary.ScoredBlocks++
			} else {
				summary.UnknownBlocks++
			}
			summary.Blocks[block] = entry
		}
		if availableWeight > 0 {
			score := weightedSum / availableWeight
			summary.WeightedScore = &score
		}
		summary.Coverage = float64(summary.ScoredBlocks) / 6
		var totalWeight float64
		for _, weight := range weights {
			totalWeight += weight
		}
		if totalWeight > 0 {
			summary.CoveredWeight = availableWeight / totalWeight
		}
		result.Competitors = append(result.Competitors, summary)
	}
	result.Limitations = []string{
		"Н/Д, unknown, missing и not_connected не входят в знаменатель и не считаются нулевой оценкой.",
		"Цена не собирается этим инструментом; передайте проверенные price-строки из существующего источника или оставьте блок price как not_connected.",
		"Личный кабинет и живой сервис могут требовать ручной проверки или авторизованного источника.",
	}
	if scoredRows == 0 {
		result.Limitations = append(result.Limitations, "Нет проверенных оценок; матрица содержит только покрытие и неизвестные блоки.")
	}
	return result, nil
}

func isUnknownMatrixStatus(status string) bool {
	switch status {
	case "unknown", "missing", "not_connected", "не собрано", "не подключён", "н/д", "н.д.", "nd":
		return true
	default:
		return false
	}
}
