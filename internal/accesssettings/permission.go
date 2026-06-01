package accesssettings

import "github.com/lkarlslund/koder/internal/toolkind"

//go:generate go tool enumer -type=PermissionMode -trimprefix=PermissionMode -transform=snake -json -text -values -output=permission_enumer.go
type PermissionMode uint8

const (
	PermissionModeAllow PermissionMode = iota
	PermissionModeAsk
	PermissionModeDeny
)

type PermissionOverride struct {
	Tool    toolkind.Kind
	Pattern string
	Action  PermissionMode
}
