// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {connect} from 'react-redux';
import {bindActionCreators} from 'redux';
import type {Dispatch} from 'redux';

import type {Group} from '@mattermost/types/groups';

import GroupTypes from 'mattermost-redux/action_types/groups';
import {getGroup} from 'mattermost-redux/selectors/entities/groups';
import type {ActionFunc, DispatchFunc, GetStateFunc} from 'mattermost-redux/types/actions';
import type {GlobalState} from 'mattermost-redux/types/store';

import GroupsInput from './groups_input';

import Client from '@/client';

function mapStateToProps(state: GlobalState, ownProps: {groups: Group[] | Array<{id: string}>}) {
    const groupIds = ownProps.groups ?
        ownProps.groups.map((group: any) => group.id) :
        [];

    const groupObjects = groupIds.map((id: string) => {
        const group = getGroup(state, id);
        return group || {id};
    });

    return {
        groups: groupObjects,
    };
}

function mapDispatchToProps(dispatch: Dispatch) {
    return {
        actions: bindActionCreators({
            searchGroups,
            getMissingGroupsByIds,
        }, dispatch),
    };
}

// Function to search groups via the plugin API
const searchGroups = (term: string): ActionFunc => {
    return async () => {
        try {
            return Client.searchGroups(term);
        } catch (error) {
            console.log(error); //eslint-disable-line no-console
            throw error;
        }
    };
};

// keep track of ongoing requests to ensure we don't try
// to query for the same groups simultaneously
const pendingGroupRequests = new Set<string>();

export function getMissingGroupsByIds(groupIds: string[]): ActionFunc {
    return async (dispatch: DispatchFunc, getState: GetStateFunc) => {
        const state = getState();
        const {groups} = state.entities.groups;
        const missingIds: string[] = [];

        groupIds.forEach((id) => {
            if (!groups[id] && !pendingGroupRequests.has(id)) {
                missingIds.push(id);
            }
        });

        if (missingIds.length === 0) {
            return {data: []};
        }

        missingIds.forEach((id) => pendingGroupRequests.add(id));

        let fetchedGroups = [];

        try {
            const promises = [];
            for (const groupId of missingIds) {
                promises.push(Client.getGroup(groupId));
            }
            fetchedGroups = await Promise.all(promises);
        } catch (error) {
            console.log(error); //eslint-disable-line no-console
            throw error;
        }

        missingIds.forEach((id) => pendingGroupRequests.delete(id));

        if (fetchedGroups.length > 0) {
            dispatch({
                type: GroupTypes.RECEIVED_GROUPS,
                data: fetchedGroups,
            });
            return {data: fetchedGroups};
        }

        return {data: []};
    };
}

export default connect(mapStateToProps, mapDispatchToProps)(GroupsInput);
