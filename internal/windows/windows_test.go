package windows

import (
	"encoding/base64"
	"encoding/xml"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dmdhrumilmistry/VaanarSena/internal/policy"
)

func TestReplyIsWellFormedAndOrdered(t *testing.T) {
	r := newReply("5", "2", "https://mdm/ManagementServer/MDM.svc", "dev-1")
	r.status("0", "SyncHdr", "212")
	start := r.next
	r.get("./DevDetail/SwV")
	r.replace(policy.SyncMLItem{LocURI: "./Device/Vendor/MSFT/Policy/Config/Camera/AllowCamera", Format: "int", Data: "0"})
	r.exec("./Device/Vendor/MSFT/DMClient/Unenroll", "VaanarSena<&>")
	chunk := r.since(start)
	out := r.finish()

	var msg struct {
		Hdr struct {
			MsgID  string `xml:"MsgID"`
			Target string `xml:"Target>LocURI"`
		} `xml:"SyncHdr"`
		Body struct {
			Status []struct{ CmdID, CmdRef, Data string } `xml:"Status"`
			Get    []struct{ CmdID string }               `xml:"Get"`
			Exec   []struct {
				Data string `xml:"Item>Data"`
			} `xml:"Exec"`
		} `xml:"SyncBody"`
	}
	if err := xml.Unmarshal([]byte(out), &msg); err != nil {
		t.Fatalf("reply is not valid XML: %v\n%s", err, out)
	}
	if msg.Hdr.MsgID != "2" || msg.Hdr.Target != "dev-1" {
		t.Errorf("header: %+v", msg.Hdr)
	}
	if msg.Body.Status[0].CmdID != "1" || msg.Body.Get[0].CmdID != "2" {
		t.Error("CmdIDs not sequential")
	}
	if msg.Body.Exec[0].Data != "VaanarSena<&>" {
		t.Errorf("exec data not escaped/round-tripped: %q", msg.Body.Exec[0].Data)
	}
	if !strings.HasPrefix(chunk, "<Get>") || strings.Contains(chunk, "<Status>") {
		t.Errorf("since() returned the wrong slice: %s", chunk)
	}
}

func TestEnrollRequestParsing(t *testing.T) {
	body := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing" xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" xmlns:wst="http://docs.oasis-open.org/ws-sx/ws-trust/200512" xmlns:ac="http://schemas.xmlsoap.org/ws/2006/12/authorization">
<s:Header><a:MessageID>urn:uuid:1</a:MessageID><wsse:Security><wsse:UsernameToken><wsse:Username>u@example.com</wsse:Username><wsse:Password>tok</wsse:Password></wsse:UsernameToken></wsse:Security></s:Header>
<s:Body><wst:RequestSecurityToken><wsse:BinarySecurityToken>` + base64.StdEncoding.EncodeToString([]byte("csr")) + `</wsse:BinarySecurityToken>
<ac:AdditionalContext><ac:ContextItem Name="DeviceID"><ac:Value>ABC123</ac:Value></ac:ContextItem><ac:ContextItem Name="EnrollmentType"><ac:Value>Device</ac:Value></ac:ContextItem></ac:AdditionalContext>
</wst:RequestSecurityToken></s:Body></s:Envelope>`
	var req enrollReq
	if _, err := readSOAP(httptest.NewRequest("POST", "/", strings.NewReader(body)), &req); err != nil {
		t.Fatal(err)
	}
	if req.Username != "u@example.com" || req.Password != "tok" || req.ctx("DeviceID") != "ABC123" || req.ctx("EnrollmentType") != "Device" {
		t.Errorf("parsed: %+v", req)
	}
}

func TestReadSOAPRejectsDTD(t *testing.T) {
	body := `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY a "aaaa">]><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"/>`
	var req soapHeader
	if _, err := readSOAP(httptest.NewRequest("POST", "/", strings.NewReader(body)), &req); err == nil {
		t.Fatal("DTD accepted")
	}
}

func TestEnrollURL(t *testing.T) {
	u := EnrollURL("https://mdm.example.com", "a+b@example.com")
	if !strings.HasPrefix(u, "ms-device-enrollment:?mode=mdm&username=a%2Bb%40example.com&servername=https%3A%2F%2Fmdm.example.com") {
		t.Errorf("url = %s", u)
	}
}
