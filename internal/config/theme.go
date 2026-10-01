package config

import "strings"

func (t *ThemeSettings) ToUIDict(appName string) map[string]any {
	displayName := appName
	if t.AppName != nil && strings.TrimSpace(*t.AppName) != "" {
		displayName = *t.AppName
	}
	var logo map[string]string
	if t.Logo != nil {
		logo = map[string]string{
			"url": t.Logo.resolvedURL(),
			"alt": t.Logo.Alt,
		}
	}
	return map[string]any{
		"appName":         displayName,
		"defaultMode":     t.DefaultMode,
		"allowModeToggle": t.AllowModeToggle,
		"logo":            logo,
		"light":           t.Light.AsCSSVars(),
		"dark":            t.Dark.AsCSSVars(),
	}
}

func (l *ThemeLogo) resolvedURL() string {
	if l.Path != nil && strings.TrimSpace(*l.Path) != "" {
		return "/ui/logo"
	}
	if l.URL != nil {
		return *l.URL
	}
	return ""
}

func (p ThemePalette) AsCSSVars() map[string]string {
	out := map[string]string{}
	add := func(name string, val *string) {
		if val == nil || strings.TrimSpace(*val) == "" {
			return
		}
		out["--"+strings.ReplaceAll(name, "_", "-")] = strings.TrimSpace(*val)
	}
	add("bg_primary", p.BgPrimary)
	add("bg_secondary", p.BgSecondary)
	add("bg_tertiary", p.BgTertiary)
	add("bg_hover", p.BgHover)
	add("border_color", p.BorderColor)
	add("text_primary", p.TextPrimary)
	add("text_secondary", p.TextSecondary)
	add("text_muted", p.TextMuted)
	add("accent_primary", p.AccentPrimary)
	add("accent_success", p.AccentSuccess)
	add("accent_warning", p.AccentWarning)
	add("accent_danger", p.AccentDanger)
	add("accent_info", p.AccentInfo)
	add("record_a", p.RecordA)
	add("record_aaaa", p.RecordAAAA)
	add("record_cname", p.RecordCNAME)
	add("record_mx", p.RecordMX)
	add("record_txt", p.RecordTXT)
	add("record_ns", p.RecordNS)
	add("record_soa", p.RecordSOA)
	add("record_ptr", p.RecordPTR)
	add("record_srv", p.RecordSRV)
	add("record_caa", p.RecordCAA)
	return out
}
