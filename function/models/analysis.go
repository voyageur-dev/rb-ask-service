package models

type Analysis struct {
	Answers     []string `json:"answers"`
	Explanation string   `json:"explanation"`
}

type CreateAnalysisRequest struct {
	Question Question `json:"question"`
	Model    string   `json:"model"`
}
