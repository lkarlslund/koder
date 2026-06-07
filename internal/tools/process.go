package tools

type ExecProcess struct {
	ProcessID   string `json:"process_id"`
	Command     string `json:"command"`
	Workdir     string `json:"workdir,omitempty"`
	Shell       string `json:"shell,omitempty"`
	TTY         bool   `json:"tty,omitempty"`
	State       string `json:"state"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	TimeoutMS   int64  `json:"timeout_ms,omitempty"`
	Output      string `json:"output,omitempty"`
	OutputBytes int    `json:"output_bytes,omitempty"`
	StdinClosed bool   `json:"stdin_closed,omitempty"`
	Lost        bool   `json:"lost,omitempty"`
}
