package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation"
	"github.com/mattermost/mattermost-plugin-content-moderation/server/moderation/azure"
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

	processor *PostProcessor
}

func (p *Plugin) OnActivate() error {
	// Ensure we have an enterprise license or a development environment
	if !pluginapi.IsEnterpriseLicensedOrDevelopment(
		p.API.GetConfig(),
		p.API.GetLicense(),
	) {
		return fmt.Errorf("this plugin requires an Enterprise license")
	}
	return p.initialize()
}

func (p *Plugin) initialize() error {
	if p.processor != nil {
		p.processor.stop()
		p.processor = nil
	}

	config := p.getConfiguration()
	if !config.Enabled {
		p.API.LogInfo("Content moderation is disabled")
		return nil
	}

	botID, err := p.API.EnsureBotUser(&model.Bot{Username: config.BotUsername})
	if err != nil {
		return errors.Wrap(err, "could not initialize bot user")
	}

	excludedUsers := config.ExcludedUserSet()

	thresholdValue, err := config.ThresholdValue()
	if err != nil {
		p.API.LogError("failed to load moderation threshold", "err", err)
		return errors.Wrap(err, "failed to load moderation threshold")
	}

	moderator, err := initModerator(p.API, config)
	if err != nil {
		return errors.Wrap(err, "failed to initialize moderator")
	}

	processor, err := newPostProcessor(
		botID, moderator, thresholdValue, excludedUsers)
	if err != nil {
		p.API.LogError("failed to create post processor", "err", err)
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
			api.LogError("failed to create Azure moderator", "err", err)
			return nil, errors.Wrap(err, "failed to create Azure moderator")
		}

		api.LogInfo("Azure AI Content Safety moderator initialized")
		return mod, nil
	default:
		return nil, errors.Errorf("unknown moderator type: %s", config.Type)
	}
}
