package domain

// UserNudgeText is the minimal content injected as a synthetic user message
// when the provider requires a user message but none is present. A single
// "." is the smallest valid user turn that satisfies the constraint without
// adding semantic content the model would act on.
const UserNudgeText = "."
