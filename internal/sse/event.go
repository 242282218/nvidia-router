package sse

type Event struct {
	Event    string
	ID       string
	Retry    string
	Data     []string
	Comments []string
}

func (e Event) IsEmpty() bool {
	return e.Event == "" && e.ID == "" && e.Retry == "" && len(e.Data) == 0 && len(e.Comments) == 0
}
