package httpapi

import (
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/miekg/dns"

	"github.com/davidgroves/dns-zone-manager-go/internal/dnsx"
)

// DNSProblem is application/problem+json with DNS extension members.
type DNSProblem struct {
	Type             string `json:"type,omitempty"`
	Title            string `json:"title,omitempty"`
	Status           int    `json:"status,omitempty"`
	Detail           string `json:"detail,omitempty"`
	Rcode            string `json:"rcode,omitempty"`
	RcodeDescription string `json:"rcode_description,omitempty"`
	Code             string `json:"code,omitempty"`
}

func (e *DNSProblem) Error() string  { return e.Detail }
func (e *DNSProblem) GetStatus() int { return e.Status }
func (e *DNSProblem) ContentType(ct string) string {
	if ct == "application/json" {
		return "application/problem+json"
	}
	return ct
}

func problem(status int, typ, title, detail string) error {
	return &huma.ErrorModel{
		Status: status,
		Type:   typ,
		Title:  title,
		Detail: detail,
	}
}

func notFound(detail string) error {
	return problem(http.StatusNotFound, "not_found", "Not Found", detail)
}

func badRequest(detail string) error {
	return problem(http.StatusBadRequest, "bad_request", "Bad Request", detail)
}

func conflict(detail string) error {
	return problem(http.StatusConflict, "conflict", "Conflict", detail)
}

func unauthorized(detail string) error {
	return problem(http.StatusUnauthorized, "unauthorized", "Unauthorized", detail)
}

func unavailable(detail string) error {
	return problem(http.StatusServiceUnavailable, "unavailable", "Service Unavailable", detail)
}

func internalErr(detail string) error {
	return problem(http.StatusInternalServerError, "internal", "Internal Server Error", detail)
}

func badGateway(detail string) error {
	return problem(http.StatusBadGateway, "bad_gateway", "Bad Gateway", detail)
}

func dnsProblem(status int, message, rcode, code string) error {
	desc := ""
	if rcode != "" {
		desc = dnsx.RcodeDescription(rcode)
	}
	return &DNSProblem{
		Type:             "dns_error",
		Title:            http.StatusText(status),
		Status:           status,
		Detail:           message,
		Rcode:            rcode,
		RcodeDescription: desc,
		Code:             code,
	}
}

func mapDNSUpdateErr(err error) error {
	if err == nil {
		return nil
	}
	var prereq *dnsx.PrerequisiteError
	if errors.As(err, &prereq) {
		return dnsProblem(http.StatusConflict, prereq.Message, prereq.RcodeText, "PREREQ_FAILED")
	}
	var upd *dnsx.UpdateError
	if errors.As(err, &upd) {
		rcode := ""
		if upd.Rcode != 0 {
			rcode = dns.RcodeToString[upd.Rcode]
		}
		return dnsProblem(http.StatusInternalServerError, upd.Message, rcode, "DDNS_FAILED")
	}
	var xfer *dnsx.ZoneTransferError
	if errors.As(err, &xfer) {
		return badGateway(xfer.Error())
	}
	var build *dnsx.UpdateBuildError
	if errors.As(err, &build) {
		return conflict(build.Message)
	}
	return internalErr(err.Error())
}
