package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func messageWorker(t *testing.T, f fixture, name, cid string) Credential {
	t.Helper()
	_, c, _, err := f.svc.DelegateSession(context.Background(), f.admin.Token, DelegateInput{PrincipalName:name, TTL:time.Hour, Grants:[]DelegatedGrant{{ContextID:cid, Permissions:PermissionRead|PermissionWrite, ExpiresAt:f.now.Add(30*time.Minute)}}})
	if err != nil { t.Fatal(err) }; return c
}

func TestTypedMessagesDuplicateNamesAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	f := setup(t, path)
	ctx := context.Background()
	a := messageWorker(t, f, "reviewer", f.context.ID)
	b := messageWorker(t, f, "reviewer", f.context.ID)
	if a.Session.ID == b.Session.ID || a.Session.PrincipalID == b.Session.PrincipalID { t.Fatal("identity collision") }
	participants, err := f.svc.ListParticipants(ctx, a.Token, f.context.ID, 100)
	if err != nil { t.Fatal(err) }
	seen := map[string]bool{}
	for _, p := range participants { if p.Name == "reviewer" { seen[p.SessionID] = true } }
	if !seen[a.Session.ID] || !seen[b.Session.ID] || len(seen)!=2 { t.Fatalf("duplicate labels not discoverable: %v", participants) }
	task, err := f.svc.CreateTask(ctx, a.Token, f.context.ID, TaskInput{Title:"review"})
	if err != nil { t.Fatal(err) }
	parent, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey:"plain", Body:"Legacy free text"})
	if err != nil { t.Fatal(err) }
	env := &MessageEnvelope{Schema:"comitia.review-request.v1", RecipientSessionIDs:[]string{b.Session.ID,a.Session.ID}, TaskID:task.ID, ReplyTo:parent.ID, Payload:json.RawMessage(`{"artifact_refs":["git:abc"],"checks":["lease fencing"]}`)}
	m, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey:"review", Body:"Please inspect", Envelope:env})
	if err != nil { t.Fatal(err) }
	if m.SessionID != a.Session.ID { t.Fatal("wrong authenticated sender") }
	env.RecipientSessionIDs=[]string{a.Session.ID,b.Session.ID}
	env.Payload=json.RawMessage(`{ "checks": ["lease fencing"], "artifact_refs": ["git:abc"] }`)
	retry, err := f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey:"review", Body:"Please inspect", Envelope:env})
	if err != nil || retry.ID != m.ID { t.Fatalf("semantic retry: %v", err) }
	env.RecipientSessionIDs=[]string{a.Session.ID}
	if _, err = f.svc.AppendMessage(ctx, a.Token, f.context.ID, MessageInput{DedupeKey:"review", Body:"Please inspect", Envelope:env}); !errors.Is(err,ErrConflict) { t.Fatalf("changed recipient dedupe: %v",err) }
	if err=f.store.Close(); err!=nil { t.Fatal(err) }
	st, err:=OpenSQLite(path); if err!=nil {t.Fatal(err)}; defer st.Close()
	svc,err:=New(Config{Store:st,Clock:func()time.Time{return *f.now}});if err!=nil {t.Fatal(err)}
	msgs,err:=svc.ReplayMessages(ctx,b.Token,f.context.ID,0,100)
	if err!=nil || len(msgs)!=2 || msgs[0].Envelope!=nil || !sameEnvelope(msgs[1].Envelope,m.Envelope) {t.Fatalf("replay lost envelope: %v %v",msgs,err)}
	got,err:=svc.GetTask(ctx,a.Token,task.ID);if err!=nil || got.Status!=TaskPending {t.Fatal("message changed task state")}
}

func TestEnvelopeScopeRevocationAndValidation(t *testing.T) {
	f:=setup(t,":memory:");defer f.store.Close();ctx:=context.Background()
	other,err:=f.svc.CreateContext(ctx,f.admin.Token,CreateContextInput{WorkspaceID:f.context.WorkspaceID,Name:"other"});if err!=nil {t.Fatal(err)}
	local:=messageWorker(t,f,"same-name",f.context.ID)
	outsider:=messageWorker(t,f,"same-name",other.ID)
	foreignTask,err:=f.svc.CreateTask(ctx,outsider.Token,other.ID,TaskInput{Title:"private"});if err!=nil {t.Fatal(err)}
	foreignMessage,err:=f.svc.AppendMessage(ctx,outsider.Token,other.ID,MessageInput{DedupeKey:"p",Body:"private"});if err!=nil {t.Fatal(err)}
	if _,err=f.svc.ListParticipants(ctx,local.Token,other.ID,100);!errors.Is(err,ErrForbidden){t.Fatalf("participant disclosure: %v",err)}
	tests:=[]struct{name string; env MessageEnvelope; want error}{
		{"name is not ID",MessageEnvelope{Schema:"comitia.discussion.v1",RecipientSessionIDs:[]string{"same-name"},Payload:json.RawMessage(`{}`)},ErrForbidden},
		{"outside recipient",MessageEnvelope{Schema:"comitia.discussion.v1",RecipientSessionIDs:[]string{outsider.Session.ID},Payload:json.RawMessage(`{}`)},ErrForbidden},
		{"outside task",MessageEnvelope{Schema:"comitia.discussion.v1",TaskID:foreignTask.ID,Payload:json.RawMessage(`{}`)},ErrForbidden},
		{"outside reply",MessageEnvelope{Schema:"comitia.discussion.v1",ReplyTo:foreignMessage.ID,Payload:json.RawMessage(`{}`)},ErrForbidden},
		{"unknown schema",MessageEnvelope{Schema:"not-registered.v1",Payload:json.RawMessage(`{}`)},ErrInvalid},
		{"unknown field",MessageEnvelope{Schema:"comitia.discussion.v1",Payload:json.RawMessage(`{"from":"owner"}`)},ErrInvalid},
		{"missing evidence",MessageEnvelope{Schema:"comitia.verification-result.v1",TaskID:"placeholder",Payload:json.RawMessage(`{"status":"passed","checks":["tests"]}`)},ErrInvalid},
		{"duplicate recipient",MessageEnvelope{Schema:"comitia.discussion.v1",RecipientSessionIDs:[]string{local.Session.ID,local.Session.ID},Payload:json.RawMessage(`{}`)},ErrInvalid},
	}
	for _,tc:=range tests {t.Run(tc.name,func(t *testing.T){if _,err:=f.svc.AppendMessage(ctx,local.Token,f.context.ID,MessageInput{DedupeKey:tc.name,Body:"text",Envelope:&tc.env});!errors.Is(err,tc.want){t.Fatalf("got %v want %v",err,tc.want)}})}
	if err=f.svc.RevokeSession(ctx,f.admin.Token,local.Session.ID);err!=nil{t.Fatal(err)}
	if _,err=f.svc.AppendMessage(ctx,f.admin.Token,f.context.ID,MessageInput{DedupeKey:"revoked",Body:"text",Envelope:&MessageEnvelope{Schema:"comitia.discussion.v1",RecipientSessionIDs:[]string{local.Session.ID},Payload:json.RawMessage(`{}`)}});!errors.Is(err,ErrForbidden){t.Fatalf("revoked recipient: %v",err)}
	ps,err:=f.svc.ListParticipants(ctx,f.admin.Token,f.context.ID,100);if err!=nil{t.Fatal(err)}
	for _,p:=range ps{if p.SessionID==local.Session.ID{t.Fatal("revoked session discovered")}}
}

func TestLegacyMessageDatabaseAndExtension(t *testing.T) {
	path:=filepath.Join(t.TempDir(),"legacy.db");f:=setup(t,path);ctx:=context.Background()
	if _,err:=f.svc.AppendMessage(ctx,f.admin.Token,f.context.ID,MessageInput{DedupeKey:"legacy",Body:"old"});err!=nil{t.Fatal(err)}
	// Model the pre-envelope schema without rewriting existing message rows.
	if _,err:=f.store.db.Exec("DROP TABLE message_envelopes");err!=nil{t.Fatal(err)}
	f.store.Close()
	st,err:=OpenSQLite(path);if err!=nil{t.Fatal(err)};defer st.Close()
	svc,err:=New(Config{Store:st,Clock:func()time.Time{return *f.now},MessageSchemas:map[string]PayloadValidator{"project.metric.v1":func(raw json.RawMessage)error{var p struct{Count int `json:"count"`};return strictPayload(raw,&p)}}});if err!=nil{t.Fatal(err)}
	if _,err=svc.AppendMessage(ctx,f.admin.Token,f.context.ID,MessageInput{DedupeKey:"custom",Envelope:&MessageEnvelope{Schema:"project.metric.v1",Payload:json.RawMessage(`{"count":2}`)}});err!=nil{t.Fatal(err)}
	msgs,err:=svc.ReplayMessages(ctx,f.admin.Token,f.context.ID,0,100);if err!=nil||len(msgs)!=2||msgs[0].Body!="old"||msgs[0].Envelope!=nil{t.Fatalf("legacy compatibility: %v %v",msgs,err)}
	if _,err=New(Config{Store:st,MessageSchemas:map[string]PayloadValidator{"comitia.discussion.v1":func(json.RawMessage)error{return nil}}});!errors.Is(err,ErrInvalid){t.Fatal("built-in override allowed")}
}
