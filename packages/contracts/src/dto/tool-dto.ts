export interface ToolDescriptorDto {
  readonly name: string;
  readonly description: string;
  readonly inputSchema: Readonly<Record<string, unknown>>;
}

export interface ToolCallResultDto {
  readonly requestId: string;
  readonly result: unknown;
}

export interface ToolListResultDto {
  readonly tools: readonly ToolDescriptorDto[];
}
