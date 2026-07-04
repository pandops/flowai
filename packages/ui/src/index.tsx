import React from 'react';

export interface HelloProps {
  name?: string;
}

export function Hello({ name = 'FlowAI' }: HelloProps) {
  return <h1 data-testid="hello">Hello, {name}!</h1>;
}
