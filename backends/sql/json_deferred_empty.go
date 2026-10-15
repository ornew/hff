package sql

// An empty row scope still validates callback contracts. The same pointer is
// used deliberately: existing observer/selection closures may capture it.
func (b *builder) validateEmptyDeferredWork(empty bool, work func() error) error {
	if empty {
		outer := *b
		*b = b.validationBuilder()
		defer func() { *b = outer }()
	}
	return work()
}
