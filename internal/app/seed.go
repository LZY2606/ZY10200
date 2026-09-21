package app

import (
	"context"

	"otdrroom/internal/otdr"
)

// SeedFixtures 在空库中导入两条固定夹具轨迹并生成初始解释。
func (s *Service) SeedFixtures(ctx context.Context) error {
	n, err := s.st.TraceCount(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for i, spec := range otdr.FixtureSpecs() {
		tr := otdr.GenerateTrace(int64(i+1), spec)
		if err := s.st.InsertTrace(ctx, tr); err != nil {
			return err
		}
		if _, err := s.RecomputeTrace(ctx, tr, tr.CreatedRev); err != nil {
			return err
		}
	}
	return s.st.LogAction(ctx, "seed_fixtures", map[string]any{"traces": len(otdr.FixtureSpecs())})
}

// ReimportFixtures 清空数据库后重新导入夹具并复核。
func (s *Service) ReimportFixtures(ctx context.Context) error {
	if err := s.st.Reset(ctx); err != nil {
		return err
	}
	if err := s.SeedFixtures(ctx); err != nil {
		return err
	}
	return s.st.LogAction(ctx, "reimport_fixtures", nil)
}
