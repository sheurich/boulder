package main

import "net/http"

// addPKIValidation01 handles a POST to register a pki-validation-01
// challenge response.
//
// POST body parameters:
//
//	"filename" - the filename under /.well-known/pki-validation/ to serve
//	"content"  - the exact response body to return
func (srv *managementServer) addPKIValidation01(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Filename string
		Content  string
	}
	if err := mustParsePOST(&request, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.Filename == "" || request.Content == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	srv.challSrv.AddPKIValidation01Challenge(request.Filename, request.Content)
	srv.log.Printf("Added pki-validation-01 challenge for filename %q - content %q\n",
		request.Filename, request.Content)
	w.WriteHeader(http.StatusOK)
}

// delPKIValidation01 handles a POST to remove a pki-validation-01
// challenge response.
//
// POST body parameters:
//
//	"filename" - the filename to delete
func (srv *managementServer) delPKIValidation01(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Filename string
	}
	if err := mustParsePOST(&request, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.Filename == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	srv.challSrv.DeletePKIValidation01Challenge(request.Filename)
	srv.log.Printf("Removed pki-validation-01 challenge for filename %q\n", request.Filename)
	w.WriteHeader(http.StatusOK)
}
