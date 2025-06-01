package models

type Analysis struct {
	Answers     []string `json:"answers"`
	Explanation string   `json:"explanation"`
}
