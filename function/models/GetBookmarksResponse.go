package models

type GetBookmarksResponse struct {
	Bookmarks map[string][]int `json:"bookmarks"`
}
