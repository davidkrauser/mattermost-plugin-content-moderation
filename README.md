# Mattermost Content Moderation Plugin

This plugin provides content moderation capabilities for Mattermost using Azure AI Content Safety APIs.

This plugin requires an active enterprise license of Mattermost.

## Overview

The Content Moderation Plugin allows Mattermost administrators to ensure all content shared on the platform meets community guidelines by automatically moderating messages and attachments.

Key features:
- Text content moderation (hate speech, sexual content, violence, self-harm)
- Configuration with a single moderation threshold
- Moderation of all users with ability to exclude specific users

## Installation

1. Download the latest release from the [releases page](https://github.com/mattermost/mattermost-plugin-content-moderation/releases)
2. Upload the plugin to your Mattermost instance via System Console > Plugin Management
3. Enable the plugin
4. Configure the plugin with your Azure AI Content Safety API key and settings

## Configuration

Configuration options:

| Setting | Description |
|---------|-------------|
| Enabled | Enable/disable content moderation |
| Type | Moderation provider type (currently only "azure" is supported) |
| Azure Endpoint | Azure API endpoint |
| Azure API Key | Azure API key (kept secure) |
| Excluded Users | User IDs to exclude from content moderation. All other users will be moderated |
| Azure Threshold | Single severity threshold applied to all content categories |

The Azure AI Content Safety API uses severity levels from 0-6:
- 0: Safe (always allowed)
- 2: Low severity (mild)
- 4: Medium severity (moderate)
- 6: High severity (severe)

## License

This repository is licensed under the [Mattermost Source Available License](LICENSE) license.
