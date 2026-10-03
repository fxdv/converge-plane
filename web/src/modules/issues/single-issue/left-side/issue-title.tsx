import { AdjustableTextArea } from '@converge/ui/components/adjustable-textarea';
import * as React from 'react';
import { useDebouncedCallback } from 'use-debounce';

interface IssueTitleProps {
  value: string;
  onChange?: (value: string) => void;
}

// When the issue title changes in the background this doesn't get updated
// TODO: fix this
export function IssueTitle({ value, onChange }: IssueTitleProps) {
  const [inputValue, setInputValue] = React.useState(value);

  const debouncedUpdates = useDebouncedCallback(async (title: string) => {
    onChange && onChange(title);
  }, 500);

  const onInputChange = (value: string) => {
    setInputValue(value);
    debouncedUpdates(value);
  };

  React.useEffect(() => {
    if (window.location.hash !== '#issue-title') {
      return;
    }
    document
      .querySelector<HTMLElement>('#issue-title [contenteditable]')
      ?.focus();
  }, []);

  return (
    <div id="issue-title">
      <AdjustableTextArea
        className="border-0 px-6 py-0 font-medium resize-none bg-transparent no-scrollbar overflow-hidden outline-hidden focus-visible:ring-0 text-xl"
        value={inputValue}
        placeholder="Issue title"
        onChange={onInputChange}
      />
    </div>
  );
}
