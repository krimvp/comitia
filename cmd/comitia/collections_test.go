package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/krimvp/comitia"
	"testing"
	"time"
)

type emptyCollectionsAPI struct{ comitia.API }

func (emptyCollectionsAPI) ListContexts(context.Context, string, string, int) ([]comitia.WorkContext, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListChildContexts(context.Context, string, string, string, int) ([]comitia.WorkContext, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListParticipants(context.Context, string, string, int) ([]comitia.Participant, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListTasks(context.Context, string, string, comitia.TaskStatus, int) ([]comitia.Task, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListInstances(context.Context, string, string) ([]comitia.InstancePresence, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ReplayMessages(context.Context, string, string, int64, int, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return nil, nil
}
func (emptyCollectionsAPI) WaitMessages(context.Context, string, string, int64, time.Duration, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return nil, nil
}

func TestEveryCLIEmptyCollectionIsArray(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		args []string
	}{{"context-list", nil}, {"context-children", []string{"--parent-id", "c"}}, {"participants", []string{"--context-id", "c"}}, {"task-list", []string{"--context-id", "c"}}, {"instances", []string{"--context-id", "c"}}, {"replay", []string{"--context-id", "c"}}, {"wait", []string{"--context-id", "c", "--timeout-seconds", "0"}}} {
		t.Run(tc.cmd, func(t *testing.T) {
			var out bytes.Buffer
			for _, group := range commandGroups {
				err := group(context.Background(), config{credential: "caller"}, emptyCollectionsAPI{}, json.NewEncoder(&out), tc.cmd, tc.args)
				if err == errUnknownCommand {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if out.String() != "[]\n" {
					t.Fatalf("empty: %s", out.String())
				}
				return
			}
			t.Fatal("unhandled command")
		})
	}
}

type delegationProbe struct {
	comitia.API
	input comitia.DelegateInput
	calls int
}

func (p *delegationProbe) DelegateSession(_ context.Context, _ string, in comitia.DelegateInput) (comitia.Principal, comitia.Credential, []comitia.Grant, error) {
	p.input = in
	p.calls++
	return comitia.Principal{}, comitia.Credential{}, []comitia.Grant{}, nil
}
func TestMultiGrantCLIUsesOneDelegation(t *testing.T) {
	p := new(delegationProbe)
	var output bytes.Buffer
	err := runSessions(context.Background(), config{credential: "caller"}, p, json.NewEncoder(&output), "delegate", []string{"--ttl", "1h", "--grant", "first:read,write:30m", "--grant", "second:read:20m"})
	if err != nil || p.calls != 1 || len(p.input.Grants) != 2 || p.input.Grants[0].ContextID != "first" || p.input.Grants[1].ContextID != "second" {
		t.Fatalf("calls=%d input=%+v err=%v", p.calls, p.input, err)
	}
}
