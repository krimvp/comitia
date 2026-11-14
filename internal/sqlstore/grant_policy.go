package sqlstore

import (
	"github.com/krimvp/comitia/internal/model"
)

func (x *sqlStore) grantPolicy() *model.GrantEvaluator {
	if x.grantEvaluator == nil {
		x.grantEvaluator = model.NewGrantEvaluator(x)
	}
	return x.grantEvaluator
}

func (x *sqlStore) invalidateGrantPolicy() { x.grantEvaluator = nil }
