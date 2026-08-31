import { Button } from '@converge/ui/components/button';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@converge/ui/components/tooltip';
import { HelpLine } from '@converge/ui/icons';
import getConfig from 'next/config';
import React from 'react';

interface BottomBarButtonProps {
  icon: React.ReactElement;
  tooltip: string;
  onClick: () => void;
  isActive?: boolean;
}

const BottomBarButton: React.FC<BottomBarButtonProps> = ({
  icon,
  tooltip,
  onClick,
  isActive,
}) => (
  <Tooltip>
    <TooltipTrigger asChild>
      <Button
        variant="link"
        onClick={onClick}
        isActive={isActive}
        className="px-3"
      >
        {icon}
      </Button>
    </TooltipTrigger>
    <TooltipContent>
      <p>{tooltip}</p>
    </TooltipContent>
  </Tooltip>
);

const { publicRuntimeConfig } = getConfig();

export function BottomBar() {
  return (
    <div className="w-full flex justify-between px-6 py-4">
      <BottomBarButton
        icon={<HelpLine size={20} />}
        tooltip="Help from docs"
        onClick={() => {
          window.open(
            publicRuntimeConfig.NEXT_PUBLIC_DOCS_URL || 'https://converge.dev',
            '_blank',
          );
        }}
      />
    </div>
  );
}
