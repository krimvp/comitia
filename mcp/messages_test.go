package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestTypedMCPUsesRealService(t *testing.T) {
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{API: svc, Credential: owner.Token}
	v, err := s.call(ctx, "list_participants", json.RawMessage(`{"context_id":"`+c.ID+`"}`))
	if err != nil || len(v.([]comitia.Participant)) != 1 {
		t.Fatalf("discovery: %v %v", v, err)
	}
	raw := json.RawMessage(`{"context_id":"` + c.ID + `","dedupe_key":"x","body":"hello","agent_instance":{"id":"worker","label":"MCP worker"},"envelope":{"schema":"comitia.discussion.v1","recipient_instances":["reviewer"],"payload":{}}}`)
	v, err = s.call(ctx, "send_message", raw)
	if err != nil || v.(comitia.Message).Envelope == nil {
		t.Fatalf("typed send: %v %v", v, err)
	}
	message := v.(comitia.Message)
	replay, err := s.call(ctx, "replay_messages", json.RawMessage(`{"context_id":"`+c.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	messages := replay.([]comitia.Message)
	if len(messages) != 1 || messages[0].AgentInstance == nil || messages[0].AgentInstance.ID != "worker" || messages[0].AgentInstance.Label != "MCP worker" || !messages[0].AgentInstanceSelfReported || len(messages[0].Envelope.RecipientInstances) != 1 {
		t.Fatalf("MCP lost claim: %v", messages)
	}
	ack, _ := json.Marshal(map[string]any{"context_id": c.ID, "sequence": message.Sequence, "agent_instance": "worker"})
	if _, err = s.call(ctx, "ack_messages", ack); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		instance string
		want     int64
	}{{"worker", message.Sequence}, {"reviewer", 0}, {"", 0}} {
		args, _ := json.Marshal(map[string]any{"context_id": c.ID, "agent_instance": check.instance})
		result, err := s.call(ctx, "message_cursor", args)
		if err != nil || result.(map[string]int64)["sequence"] != check.want {
			t.Fatalf("MCP cursor %q: %v %v", check.instance, result, err)
		}
	}
	bad := json.RawMessage(`{"context_id":"` + c.ID + `","dedupe_key":"bad","body":"hello","envelope":{"schema":"comitia.discussion.v1","from":"fake","payload":{}}}`)
	if _, err = s.call(ctx, "send_message", bad); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("spoof field: %v", err)
	}
	if _, err = s.call(ctx, "list_participants", json.RawMessage(`{"context_id":"`+c.ID+`","limit":0}`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("zero limit: %v", err)
	}
}
