package exectool

import "testing"

func TestCommandNormalizeArgs(t *testing.T) {
	args, err := (commandTool{}).NormalizeArgs(map[string]string{
		"cmd":           "sleep 1",
		"tty":           "true",
		"yield_time_ms": "250",
	})
	if err != nil {
		t.Fatalf("normalize args: %v", err)
	}
	if args["cmd"] != "sleep 1" || args["tty"] != "true" || args["yield_time_ms"] != "250" {
		t.Fatalf("unexpected normalized args: %#v", args)
	}
}

func TestWriteStdinRequiresAction(t *testing.T) {
	if _, err := (writeStdinTool{}).NormalizeArgs(map[string]string{"process_id": "exec_1"}); err == nil {
		t.Fatal("expected missing chars/close_stdin error")
	}
}
