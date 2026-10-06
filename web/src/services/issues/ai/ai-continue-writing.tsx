import { useState } from 'react';

import { useMutation } from 'common/lib/react-query';

import { AI_CONTINUE_WRITING_API } from './ai-continue-writing-path';

export function useAIContinueWritingMutation() {
  const [responses, setResponses] = useState('');
  const [streaming, setStreaming] = useState(false);
  const { mutate, isPending: apiloading } = useMutation({
    mutationFn: async ({
      description,
      workspaceId,
      userInput,
    }: {
      description: string;
      workspaceId: string;
      userInput: string;
    }) => {
      setResponses('');

      const response = await fetch(AI_CONTINUE_WRITING_API, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        credentials: 'include',
        body: JSON.stringify({ description, workspaceId, userInput }),
      });

      if (!response.ok) {
        throw new Error(`AI continue-writing failed (${response.status})`);
      }

      if (!response.body) {
        throw new Error('ReadableStream not supported in this browser.');
      }

      const reader = response.body.getReader();
      return reader;
    },
    onSuccess: (reader) => {
      setStreaming(true);
      readStream(reader);
    },
  });

  async function readStream(reader: ReadableStreamDefaultReader) {
    async function read() {
      while (true) {
        const { done, value } = await reader.read();
        if (done) {
          setStreaming(false);
          return;
        }

        const chunk = new TextDecoder('utf-8').decode(value, { stream: true });

        setResponses((prevDescription) => prevDescription + chunk);
      }
    }
    read();
  }

  return { responses, mutate, isPending: streaming || apiloading };
}
