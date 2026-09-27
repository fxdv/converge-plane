import type {
  NextComponentType,
  NextPageContext,
  NextLayoutComponentType,
} from 'next';
import type { AppProps } from 'next/app';

declare module 'next' {
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type
  type NextLayoutComponentType<P = {}> = NextComponentType<
    NextPageContext,
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    any,
    P
  > & {
    getLayout?: (page: ReactNode) => ReactNode;
  };
}

declare module 'next/app' {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-object-type
  type AppLayoutProps<P = {}> = AppProps & {
    Component: NextLayoutComponentType;
  };
}
