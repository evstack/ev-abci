package adapter

// Option is a functional option for configuring the Adapter.
type Option func(*Adapter)

// WithMetrics sets custom metrics for the Adapter.
func WithMetrics(m *Metrics) Option {
	return func(a *Adapter) {
		a.metrics = m
	}
}

// WithBlockFilter sets a custom block publisher for the Adapter.
func WithBlockFilter(publisher BlockFilter) Option {
	return func(a *Adapter) {
		a.blockFilter = publisher
	}
}
