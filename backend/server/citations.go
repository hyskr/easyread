package server

import (
	"github.com/hyskr/easyread/backend/importer"
	"io"
	"net/http"
	"strings"
)

func (a *App) importCitation(w http.ResponseWriter, r *http.Request) error {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/p/"), "/")
	source, err := a.Library.Workspace(parts[0])
	if err != nil {
		return err
	}
	var data []byte
	var name, refID, fingerprint string
	var meta Object
	if parts[1] == "citation-import-pdf" {
		r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
		data, err = io.ReadAll(r.Body)
		name = r.URL.Query().Get("name")
		refID = r.URL.Query().Get("ref_id")
		fingerprint = r.URL.Query().Get("fingerprint")
	} else {
		var b Object
		b, err = body(w, r)
		if err != nil {
			return err
		}
		refID = text(b["ref_id"])
		fingerprint = text(b["fingerprint"])
		data, name, meta, err = importer.Fetch(r.Context(), text(b["ref"]))
	}
	if err != nil {
		return err
	}
	ws, fresh, err := a.Library.ImportPDF(data, name, meta)
	if err != nil {
		return err
	}
	result := Object{"id": ws.ID(), "new": fresh}
	if err = a.Library.LinkReference(source, refID, fingerprint, ws.ID()); err != nil {
		result["citation_error"] = err.Error()
	}
	paper, err := ws.Load("paper")
	if err != nil {
		return err
	}
	if fresh || len(array(object(paper["meta"])["pages"])) == 0 {
		if _, err = a.Jobs.Prepare(ws); err != nil {
			return err
		}
		result["queued"] = true
	}
	response(w, 200, result)
	return nil
}
