package comitia

func (x *sqlStore) grantPolicy() *GrantEvaluator {
	if x.grantEvaluator == nil {
		x.grantEvaluator = NewGrantEvaluator(x)
	}
	return x.grantEvaluator
}

func (x *sqlStore) invalidateGrantPolicy() { x.grantEvaluator = nil }
