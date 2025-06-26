package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation"
	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation/azure"
	"github.com/mattermost/mattermost-plugin-content-moderation/server/store/sqlstore"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/pkg/errors"
)

const moderationTimeout = 10 * time.Second

type Plugin struct {
	plugin.MattermostPlugin

	configurationLock sync.RWMutex
	configuration     *configuration

	sqlStore  *sqlstore.SQLStore
	processor *PostProcessor
}

func (p *Plugin) OnActivate() error {
	if !pluginapi.IsEnterpriseLicensedOrDevelopment(
		p.API.GetConfig(),
		p.API.GetLicense(),
	) {
		return fmt.Errorf("this plugin requires an Enterprise license")
	}

	client := pluginapi.NewClient(p.API, p.Driver)
	SQLStore, err := sqlstore.New(client.Store, &client.Log)
	if err != nil {
		p.API.LogError("Cannot create SQLStore", "err", err)
		return err
	}
	p.sqlStore = SQLStore

	config := p.getConfiguration()
	if err := p.initialize(config); err != nil {
		p.API.LogError("Cannot initialize plugin", "err", err)
		return nil
	}

	return nil
}

func (p *Plugin) initialize(config *configuration) error {
	if p.processor != nil {
		p.processor.stop()
		p.processor = nil
	}

	if !config.Enabled {
		p.API.LogInfo("Content moderation is disabled")
		return nil
	}

	moderator, err := initModerator(p.API, config)
	if err != nil {
		return errors.Wrap(err, "failed to initialize moderator")
	}

	excludedUsers := config.ExcludedUserSet()
	excludedChannels := config.ExcludedChannelSet()

	thresholdValue, err := config.ThresholdValue()
	if err != nil {
		return errors.Wrap(err, "failed to load moderation threshold")
	}

	botID, err := p.API.EnsureBotUser(&model.Bot{Username: config.BotUsername})
	if err != nil {
		return errors.Wrap(err, "could not initialize bot user")
	}

	processor, err := newPostProcessor(
		botID, moderator, config.AuditLoggingEnabled, thresholdValue, excludedUsers, excludedChannels)
	if err != nil {
		return errors.Wrap(err, "failed to create post processor")
	}
	p.processor = processor
	p.processor.start(p.API)

	return nil
}

func initModerator(api plugin.API, config *configuration) (moderation.Moderator, error) {
	switch config.Type {
	case "azure":
		azureConfig := &moderation.Config{
			Endpoint: config.Endpoint,
			APIKey:   config.APIKey,
		}

		mod, err := azure.New(azureConfig)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create Azure moderator")
		}

		api.LogInfo("Azure AI Content Safety moderator initialized")
		return mod, nil
	default:
		return nil, errors.Errorf("unknown moderator type: %s", config.Type)
	}
}

// ServeHTTP handles HTTP requests to the plugin
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
		return
	}

	router := mux.NewRouter()
	router.HandleFunc("/api/v1/channels/search", p.searchChannels).Methods(http.MethodGet)
	router.ServeHTTP(w, r)
}

// searchChannels handles the channel search API endpoint
func (p *Plugin) searchChannels(w http.ResponseWriter, r *http.Request) {
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	if prefix == "" {
		http.Error(w, "missing search prefix", http.StatusBadRequest)
		return
	}

	channels, err := p.sqlStore.SearchChannelsByPrefix(prefix)
	if err != nil {
		http.Error(w, "failed to search channels", http.StatusInternalServerError)
		p.API.LogError("failed to search channels", "error", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(channels); err != nil {
		p.API.LogError("failed to write http response", "error", err.Error())
	}
}
