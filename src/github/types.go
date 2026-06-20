package github

type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
		Type      string `json:"type"`
	} `json:"account"`
}

type session struct {
	Token        string        `json:"token"`
	Installation *Installation `json:"installation"`
}
