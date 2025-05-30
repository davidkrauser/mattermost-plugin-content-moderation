// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {connect} from 'react-redux';

import type {Group} from '@mattermost/types/groups';

import GroupsInputComponent from '../groups_input';

const GroupsInput = GroupsInputComponent as any;

interface GroupSettingsProps {
    id?: string;
    value?: string;
    onChange?: (id: string, value: string) => void;
}

interface GroupSettingsState {
    groups: Array<{id: string}>;
}

class GroupSettings extends React.Component<GroupSettingsProps, GroupSettingsState> {
    constructor(props: GroupSettingsProps) {
        super(props);
        this.state = {
            groups: [],
        };
    }

    componentDidMount() {
        this.initializeGroups(this.props.value || '');
    }

    componentDidUpdate(prevProps: GroupSettingsProps) {
        if (prevProps.value !== this.props.value) {
            this.initializeGroups(this.props.value || '');
        }
    }

    initializeGroups = (value: string) => {
        if (value) {
            const groupIds = value.split(',').map((id) => id.trim()).filter((id) => id);
            if (groupIds.length > 0) {
                // Just create group objects with IDs
                // The GroupsInput component will fetch the full group details
                const groupObjects = groupIds.map((id) => ({id}));
                this.setState({groups: groupObjects});
            } else {
                this.setState({groups: []});
            }
        } else {
            this.setState({groups: []});
        }
    };

    handleChange = (selectedGroups: Group[]) => {
        // Save the selected group IDs as a comma-separated string
        if (!selectedGroups || !this.props.onChange || !this.props.id) {
            return;
        }
        const groupIds = selectedGroups.map((group) => group?.id).filter(Boolean).join(',');
        this.props.onChange(this.props.id, groupIds);
    };

    render() {
        if (!this.props.id) {
            return null;
        }
        return (
            <GroupsInput
                placeholder='Search for LDAP groups to exclude from moderation'
                groups={this.state.groups}
                onChange={this.handleChange}
            />
        );
    }
}

export default connect(null, null)(GroupSettings);
