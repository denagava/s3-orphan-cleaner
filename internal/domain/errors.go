package domain

type DeleteFailure struct {
	Key     string
	Code    string
	Message string
}

type PartialDeleteError interface {
	error
	Failures() []DeleteFailure
}
