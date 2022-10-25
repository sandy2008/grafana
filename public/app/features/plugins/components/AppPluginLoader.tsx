import { createSelector } from '@reduxjs/toolkit';
import React, { useState } from 'react';
import { useLocation, useParams } from 'react-router-dom';

import { NavModel } from '@grafana/data';
import { getWarningNav } from 'app/angular/services/nav_model_srv';
import { Page } from 'app/core/components/Page/Page';
import PageLoader from 'app/core/components/PageLoader/PageLoader';
import { StoreState, useSelector } from 'app/types';

import { useImportAppPlugin } from '../hooks/useImportAppPlugin';
import { buildPluginSectionNav } from '../utils';

import { buildPluginPageContext, PluginPageContext } from './PluginPageContext';

type AppPluginLoaderProps = {
  // The id of the app plugin to be loaded
  id: string;
  navId: string;
  // The base URL path - defaults to the current path
  basePath?: string;
};

// This component can be used to render an app-plugin based on its plugin ID.
export const AppPluginLoader = ({ id, navId, basePath }: AppPluginLoaderProps) => {
  const [nav, setNav] = useState<NavModel | null>(null);
  const { value: plugin, error, loading } = useImportAppPlugin(id);
  const queryParams = useParams();
  const location = useLocation();

  const context = useSelector(
    createSelector(
      (state: StoreState) => state.navIndex,
      (navIndex) => buildPluginPageContext(buildPluginSectionNav(location, nav, navIndex, id)) // /a/:pluginId
    )
  );

  if (error) {
    return <Page.Header navItem={getWarningNav(error.message, error.stack).main} />;
  }

  return (
    <PluginPageContext.Provider value={context}>
      {loading && <PageLoader />}
      {nav && <Page.Header navItem={nav.main} />}
      {!loading && plugin && plugin.root && (
        <plugin.root
          meta={plugin.meta}
          basename={basePath || location.pathname}
          onNavChanged={setNav}
          query={queryParams}
          path={location.pathname}
        />
      )}
    </PluginPageContext.Provider>
  );
};
