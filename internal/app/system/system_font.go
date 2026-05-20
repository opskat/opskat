package system

import "github.com/opskat/opskat/internal/service/font_svc"

// ListSystemFonts returns installed font family names for settings font picker.
func (s *System) ListSystemFonts() ([]string, error) {
	return font_svc.ListFamilies(s.ctx)
}
