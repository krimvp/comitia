package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestTypedHTTPMessagesAndParticipantDiscovery(t *testing.T) {
	st,err:=comitia.OpenSQLite(t.TempDir()+"/http.db");if err!=nil{t.Fatal(err)};defer st.Close()
	svc,err:=comitia.New(comitia.Config{Store:st,BootstrapCredential:"local"});if err!=nil{t.Fatal(err)}
	ctx:=context.Background()
	_,owner,err:=svc.Bootstrap(ctx,comitia.BootstrapInput{Credential:"local",AdminName:"owner",SessionTTL:time.Hour});if err!=nil{t.Fatal(err)}
	w,err:=svc.CreateWorkspace(ctx,owner.Token,"w");if err!=nil{t.Fatal(err)}
	c,err:=svc.CreateContext(ctx,owner.Token,comitia.CreateContextInput{WorkspaceID:w.ID,Name:"c"});if err!=nil{t.Fatal(err)}
	ts:=httptest.NewServer(NewHandler(svc));defer ts.Close()
	client,err:=NewClient(ts.URL,ts.Client());if err!=nil{t.Fatal(err)}
	ps,err:=client.ListParticipants(ctx,owner.Token,c.ID,100);if err!=nil||len(ps)!=1||ps[0].SessionID!=owner.Session.ID{t.Fatalf("participants: %v %v",ps,err)}
	m,err:=client.AppendMessage(ctx,owner.Token,c.ID,comitia.MessageInput{DedupeKey:"typed",Body:"hello",Envelope:&comitia.MessageEnvelope{Schema:"comitia.discussion.v1",RecipientSessionIDs:[]string{ps[0].SessionID},Payload:json.RawMessage(`{}`)}});if err!=nil{t.Fatal(err)}
	msgs,err:=client.ReplayMessages(ctx,owner.Token,c.ID,0,100);if err!=nil||len(msgs)!=1||msgs[0].Envelope==nil||msgs[0].ID!=m.ID{t.Fatalf("replay: %v %v",msgs,err)}
	if _,err=client.ListParticipants(ctx,"invalid",c.ID,100);!errors.Is(err,comitia.ErrUnauthenticated){t.Fatalf("unauthenticated discovery: %v",err)}
	// Caller-supplied sender metadata is not part of the envelope contract.
	body:=`{"context_id":"`+c.ID+`","message":{"DedupeKey":"spoof","Body":"x","envelope":{"schema":"comitia.discussion.v1","from":"owner","payload":{}}}}`
	req,_:=http.NewRequest(http.MethodPost,ts.URL+"/v1/messages/append",strings.NewReader(body));req.Header.Set("Authorization","Bearer "+owner.Token);req.Header.Set("Content-Type","application/json")
	res,err:=ts.Client().Do(req);if err!=nil{t.Fatal(err)};defer res.Body.Close()
	if res.StatusCode!=http.StatusBadRequest{t.Fatalf("sender spoof field accepted: %d",res.StatusCode)}
}
