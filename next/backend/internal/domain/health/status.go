package health

// Status is the transport-independent health state exposed by the scaffold.
type Status struct {
	State   string `json:"status"`
	Version string `json:"version"`
}
