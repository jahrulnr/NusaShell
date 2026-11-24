// Account is a tenant record.
export interface Account {
  id: ID;
  name: string;
}

// ID is a branded string.
export type ID = string & { readonly brand: unique symbol };

// BaseRepo persists entities.
export abstract class BaseRepo<T> {
  abstract save(t: T): Promise<void>;
  async flush(): Promise<void> {}
}

export namespace Auth {
  export const realm = "internal";
}

function hashKey(a: Account): string {
  return a.id;
}

interface Local {
  tmp: boolean;
}
