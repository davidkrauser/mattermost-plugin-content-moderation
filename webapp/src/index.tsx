// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import GroupSettings from '@/components/admin_settings/group_settings';
import UserSettings from '@/components/admin_settings/user_settings';
import manifest from '@/manifest';
import type {PluginRegistry} from '@/types/mattermost-webapp';

export default class Plugin {
    public async initialize(registry: PluginRegistry) {
        registry.registerAdminConsoleCustomSetting('excludedUsers', UserSettings, {showTitle: true});
        registry.registerAdminConsoleCustomSetting('excludedGroups', GroupSettings, {showTitle: true});
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void;
    }
}

window.registerPlugin(manifest.id, new Plugin());
