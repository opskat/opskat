package extstore_svc

import (
	"errors"
	"strings"

	"github.com/opskat/opskat/internal/pkg/appversion"
	"github.com/opskat/opskat/pkg/extension"
	"github.com/opskat/opskat/pkg/extstore"
)

// fallbackLang is the index language shown when the app language has no text.
const fallbackLang = "en"

// State is what the store page shows.
type State struct {
	// UpdatedAt is when the shown index was fetched (unix ms); 0 before the
	// first successful refresh.
	UpdatedAt int64 `json:"updatedAt"`
	// Verified is true when Extensions come from an index whose signature
	// verified — always, once anything is shown.
	Verified bool `json:"verified"`
	// Error is the latest refresh's failure; when set, Extensions is empty.
	Error *StateError `json:"error"`
	// Extensions are the store cards, in index order.
	Extensions []Card `json:"extensions"`
	// Updates are the cards of the installed extensions the last verified index
	// offers an update for. Unlike Extensions they survive a failed refresh: that
	// index was verified, and a store install still installs from it.
	Updates []Card `json:"updates"`
}

// StateError is a refresh failure as the store shows it.
type StateError struct {
	Kind    ErrorKind `json:"kind"`
	Message string    `json:"message"`
}

// Card is one extension in the store.
type Card struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	// Version is the version the card is about: the offer for install /
	// update, the installed version for installed, the newest published
	// version for unavailable.
	Version string `json:"version"`
	// InstalledVersion is "" when not installed.
	InstalledVersion string          `json:"installedVersion"`
	Action           extstore.Action `json:"action"`
	// Unavailable says why, for ActionUnavailable only.
	Unavailable *Unavailable `json:"unavailable"`
	// Capabilities and Size are those of the index entry for Version (zero
	// when an installed version is not in the index).
	Capabilities extension.Capabilities `json:"capabilities"`
	Size         int64                  `json:"size"`
}

// Unavailable is why no version can be installed; the remedy is always to
// update OpsKat. HostABI / MinAppVersion / SourceType are what the newest
// version needs, whichever the reason names.
type Unavailable struct {
	Reason        UnavailableReason `json:"reason"`
	HostABI       string            `json:"hostABI"`
	MinAppVersion string            `json:"minAppVersion"`
	SourceType    string            `json:"sourceType"`
}

// State describes the store in lang (display names and descriptions follow lang,
// falling back to en per field, then to the extension name).
func (s *Service) State(lang string) State {
	s.mu.Lock()
	idx, fetchedAt, lastErr := s.index, s.fetchedAt, s.lastErr
	s.mu.Unlock()

	st := State{Extensions: []Card{}, Updates: []Card{}}
	if fetchedAt.IsZero() {
		if lastErr != nil {
			st.Error = &StateError{Kind: lastErr.Kind, Message: lastErr.Err.Error()}
		}
		return st
	}
	app := s.opts.App()
	installed := s.opts.InstalledVersions()
	cards := make([]Card, 0, len(idx.Extensions))
	for _, ext := range idx.Extensions {
		c := card(ext, lang, app, installed[ext.Name])
		cards = append(cards, c)
		if c.Action == extstore.ActionUpdate {
			st.Updates = append(st.Updates, c)
		}
	}
	if lastErr != nil {
		st.Error = &StateError{Kind: lastErr.Kind, Message: lastErr.Err.Error()}
		return st
	}
	st.UpdatedAt = fetchedAt.UnixMilli()
	st.Verified = true
	st.Extensions = cards
	return st
}

func card(ext extstore.Extension, lang string, app appversion.Info, installed string) Card {
	c := Card{Name: ext.Name, Icon: ext.Icon, InstalledVersion: installed}
	c.DisplayName, c.Description = display(ext, lang)

	choice, reason := extstore.SelectInstallable(ext, app, installed)
	c.Action = choice.Action
	var shown *extstore.Version
	switch choice.Action {
	case extstore.ActionInstall, extstore.ActionUpdate:
		shown = choice.Version
	case extstore.ActionInstalled:
		c.Version = installed
		shown = findVersion(ext, installed)
	case extstore.ActionUnavailable:
		shown = newest(ext)
		c.Unavailable = unavailable(reason)
	}
	if shown != nil {
		c.Version, c.Capabilities, c.Size = shown.Version, shown.Capabilities, shown.Size
	}
	return c
}

// display picks ext's text for lang. Language tags compare case-insensitively:
// the index keys text by "zh-CN" while the desktop's own language is "zh-cn".
func display(ext extstore.Extension, lang string) (name, description string) {
	d, en := displayFor(ext, lang), displayFor(ext, fallbackLang)
	name, description = d.Name, d.Description
	if name == "" {
		name = en.Name
	}
	if name == "" {
		name = ext.Name
	}
	if description == "" {
		description = en.Description
	}
	return name, description
}

func displayFor(ext extstore.Extension, lang string) extstore.Display {
	if d, ok := ext.Display[lang]; ok {
		return d
	}
	for tag, d := range ext.Display {
		if strings.EqualFold(tag, lang) {
			return d
		}
	}
	return extstore.Display{}
}

func findVersion(ext extstore.Extension, version string) *extstore.Version {
	for i := range ext.Versions {
		if ext.Versions[i].Version == version {
			return &ext.Versions[i]
		}
	}
	return nil
}

func newest(ext extstore.Extension) *extstore.Version {
	var out *extstore.Version
	for i := range ext.Versions {
		if out == nil || appversion.Compare(ext.Versions[i].Version, out.Version) > 0 {
			out = &ext.Versions[i]
		}
	}
	return out
}

// unavailable maps SelectInstallable's reason, which is always an
// *extstore.UnsupportedSourceError or an *extension.IncompatibleError.
func unavailable(reason error) *Unavailable {
	var se *extstore.UnsupportedSourceError
	if errors.As(reason, &se) {
		return &Unavailable{Reason: ReasonSource, SourceType: se.Type}
	}
	var ie *extension.IncompatibleError
	errors.As(reason, &ie)
	return &Unavailable{Reason: UnavailableReason(ie.Reason), HostABI: ie.HostABI, MinAppVersion: ie.MinAppVersion}
}
