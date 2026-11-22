package service

import "context"

// AuthorizeOfficialRuntime repeats current model access checks before the
// runtime adapter retrieves an employee credential.
func (s *ModelService) AuthorizeOfficialRuntime(ctx context.Context, w, actor, agent string) error {
	return s.Store.WithWorkspace(ctx, w, func(tx ModelTx) error {
		return authorizeRuntime(ctx, tx, actor, agent, func() error { return modelAuthorize(ctx, tx, actor, false) })
	})
}

// RuntimeModelKey is consumed only by the authorized execution configuration
// adapter. A changed target cannot receive the previous Session's credential.
func (s *ModelService) RuntimeModelKey(ctx context.Context, w, actor, id, protocol, baseURL, modelID string) (string, error) {
	var key string
	err := s.Store.WithWorkspace(ctx, w, func(tx ModelTx) error {
		if err := authorizeRuntime(ctx, tx, actor, "", func() error { return modelAuthorize(ctx, tx, actor, false) }); err != nil {
			return err
		}
		v, err := tx.Get(ctx, id)
		if err != nil {
			return err
		}
		if !v.Enabled || !v.Validated || v.Protocol != protocol || v.BaseURL != baseURL || v.ModelID != modelID {
			return Fail("configuration_unusable", 422, "原会话模型目标已变更、停用或撤销")
		}
		key, err = s.decrypt(w, v)
		return err
	})
	return key, err
}
