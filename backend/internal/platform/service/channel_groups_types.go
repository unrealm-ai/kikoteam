package service

type ChannelGroupItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type ChannelGroupPage struct {
	Items      []ChannelGroupItem `json:"items"`
	NextCursor string             `json:"next_cursor"`
}
