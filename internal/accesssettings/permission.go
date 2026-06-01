package accesssettings

//go:generate go tool enumer -type=PermissionMode -trimprefix=PermissionMode -transform=snake -json -text -values -output=permission_enumer.go
type PermissionMode uint8

const (
	PermissionModeAllow PermissionMode = iota
	PermissionModeAsk
	PermissionModeDeny
)
