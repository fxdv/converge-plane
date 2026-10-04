import './global.css';
import '@converge/ui/index.css';
import '@converge/ui/global.css';
import type { NextComponentType } from 'next';
import type { AppContext, AppInitialProps, AppLayoutProps } from 'next/app';

import { ThemeProvider } from '@converge/ui/components/theme-provider';
import { Toaster } from '@converge/ui/components/toaster';
import { TooltipProvider } from '@converge/ui/components/tooltip';
import { cn } from '@converge/ui/lib/utils';
import { GeistMono } from 'geist/font/mono';
import { GeistSans } from 'geist/font/sans';
import TimeAgo from 'javascript-time-ago';
import en from 'javascript-time-ago/locale/en';
import React from 'react';
import { HotkeysProvider } from 'react-hotkeys-hook';
import { HydrationBoundary, QueryClientProvider } from 'common/lib/react-query';

import { initSession } from 'common/init-config';
import { DocumentTitle } from 'common/layouts/app-layout/document-title';
import { useGetQueryClient } from 'common/lib/react-query-client';
import { SCOPES } from 'common/scopes';

import { StoreContext, storeContextStore } from 'store/global-context-provider';

initSession();

TimeAgo.addDefaultLocale(en);

export const MyApp: NextComponentType<
  AppContext,
  AppInitialProps,
  AppLayoutProps
> = ({
  Component,
  pageProps: { dehydratedState, ...pageProps },
}: AppLayoutProps) => {
  const queryClientRef = useGetQueryClient();
  const getLayout = Component.getLayout || ((page: React.ReactNode) => page);

  return (
    <ThemeProvider
      attribute="class"
      defaultTheme="light"
      enableSystem
      disableTransitionOnChange
    >
      <HotkeysProvider initiallyActiveScopes={[SCOPES.Global]}>
        <TooltipProvider delayDuration={500}>
          <StoreContext.Provider value={storeContextStore}>
            <QueryClientProvider client={queryClientRef.current}>
              <HydrationBoundary state={dehydratedState}>
                <div
                  className={cn(
                    'min-h-screen font-sans antialiased flex',
                    GeistSans.variable,
                    GeistMono.variable,
                  )}
                >
                  <DocumentTitle />
                  {getLayout(<Component {...pageProps} />)}
                </div>

                <Toaster />
              </HydrationBoundary>
            </QueryClientProvider>
          </StoreContext.Provider>
        </TooltipProvider>
      </HotkeysProvider>
    </ThemeProvider>
  );
};

export default MyApp;
