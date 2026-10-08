import { useToast } from '@converge/ui/components/use-toast';
import { useRouter } from 'next/router';
import * as React from 'react';

import { QueryCache, QueryClient } from 'common/lib/react-query';

export const useGetQueryClient = () => {
  const router = useRouter();
  const { toast } = useToast();

  return React.useRef(
    new QueryClient({
      queryCache: new QueryCache({
        // eslint-disable-next-line @typescript-eslint/no-explicit-any
        onError: (error: any) => {
          if (error?.resStatus === 403) {
            toast({
              variant: 'destructive',
              title: 'This page is closed to you',
              description: "You don't have access. Return to the board.",
            });
            router.push('/');
          }
        },
      }),
    }),
  );
};
