package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	// All HTTP endpoints of this plugin require a logged-in user.
	userID := r.Header.Get("Mattermost-User-ID")
	if userID == "" {
		http.Error(w, "Not authorized", http.StatusUnauthorized)
		return
	}

	// All HTTP endpoints of this plugin require the user to be a System Admin
	if !p.API.HasPermissionTo(userID, model.PermissionManageSystem) {
		http.Error(w, "Not authorized", http.StatusUnauthorized)
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/groups/search", p.searchLDAPGroups).Methods(http.MethodGet)
	router.ServeHTTP(w, r)
}

func (p *Plugin) searchLDAPGroups(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	if prefix == "" {
		http.Error(w, "missing search prefix", http.StatusBadRequest)
		return
	}

	groups, err := p.sqlStore.SearchLDAPGroupsByPrefix(prefix)
	if err != nil {
		http.Error(w, "failed to search groups", http.StatusInternalServerError)
		p.API.LogError("failed to search groups", "error", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(groups); err != nil {
		p.API.LogError("failed to write http response", "error", err.Error())
	}
}
