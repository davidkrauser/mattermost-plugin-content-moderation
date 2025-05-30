// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import debounce from 'lodash/debounce';
import React, {useEffect} from 'react';
import type {MultiValue, StylesConfig} from 'react-select';
import AsyncSelect from 'react-select/async';
import type {ActionFunc} from 'mattermost-redux/types/actions';

import type {Group} from '@mattermost/types/groups';

interface GroupsInputProps {
    placeholder?: string;
    groups: Group[] | Array<{id: string}>;
    onChange?: (groups: Group[]) => void;
    actions: {
        searchGroups: (term: string) => Promise<Group[]>;
        getMissingGroupsByIds: (groupIds: string[]) => ActionFunc;
    };
}

// GroupsInput searches and selects LDAP groups displayed by display name.
// Groups prop can handle the group object or strings directly if the group object is not available.
// Returns the selected groups ids in the `OnChange` value parameter.
export default function GroupsInput(props: GroupsInputProps) {
    // Extract the group IDs from the props.groups array
    const groupIds = React.useMemo(() => {
        if (!props.groups || !props.groups.length) {
            return [];
        }

        return props.groups.map((group: Group | {id: string}) => {
            return group?.id;
        }).filter((id): id is string => Boolean(id));
    }, [props.groups]);

    // Fetch missing groups whenever groupIds changes
    useEffect(() => {
        if (groupIds.length > 0) {
            props.actions.getMissingGroupsByIds(groupIds);
        }
    }, [groupIds, props.actions]);

    const onChange = (newValue: MultiValue<string | Group | {id: string}>) => {
        if (props.onChange) {
            props.onChange(newValue as unknown as Group[]);
        }
    };

    const getOptionValue = (group: Group | {id: string} | string) => {
        if (typeof group === 'object' && group?.id) {
            return group.id;
        }
        return group as string;
    };

    const formatOptionLabel = (option: Group | {id: string} | string) => {
        if (typeof option === 'object') {
            if ('display_name' in option && option.display_name && 'name' in option && option.name) {
                return (
                    <React.Fragment>
                        {`${option.display_name} (@${option.name})`}
                    </React.Fragment>
                );
            }

            if ('display_name' in option && option.display_name) {
                return (
                    <React.Fragment>
                        {option.display_name}
                    </React.Fragment>
                );
            }

            if (option?.id) {
                return option.id;
            }
        }

        return option as string;
    };

    const debouncedSearchGroups = debounce((term: string, callback: (data: Group[]) => void) => {
        props.actions.searchGroups(term).
            then((data) => {
                callback(data);
            }).
            catch(() => {
                // eslint-disable-next-line no-console
                console.error('Error searching groups in custom attribute settings dropdown.');
                callback([]);
            });
    }, 150);

    const groupsLoader = (term: string, callback: (data: Group[]) => void) => {
        try {
            debouncedSearchGroups(term, callback);
        } catch (error) {
            // eslint-disable-next-line no-console
            console.error(error);
            callback([]);
        }
    };

    const keyDownHandler = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter') {
            e.stopPropagation();
        }
    };

    return (
        <AsyncSelect
            isMulti={true}
            cacheOptions={true}
            defaultOptions={false}
            loadOptions={groupsLoader}
            onChange={onChange}
            getOptionValue={getOptionValue}
            formatOptionLabel={formatOptionLabel}
            defaultMenuIsOpen={false}
            openMenuOnClick={false}
            isClearable={false}
            placeholder={props.placeholder}
            value={props.groups}
            components={{DropdownIndicator: () => null, IndicatorSeparator: () => null}}
            styles={customStyles}
            menuPortalTarget={document.body}
            menuPosition={'fixed'}
            onKeyDown={keyDownHandler}
        />
    );
}


const customStyles: StylesConfig<any, true> = {
    container: (baseStyles) => ({
        ...baseStyles,
    }),
    control: (baseStyles) => ({
        ...baseStyles,
        minHeight: '46px',
    }),
    menuPortal: (baseStyles) => ({
        ...baseStyles,
        zIndex: 9999,
    }),
    multiValue: (baseStyles) => ({
        ...baseStyles,
        borderRadius: '50px',
    }),
};
