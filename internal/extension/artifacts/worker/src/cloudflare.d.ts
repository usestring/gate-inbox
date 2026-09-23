/**
 * The slice of the Workers runtime this store uses.
 *
 * Declared here rather than pulled from @cloudflare/workers-types because
 * this directory installs no node_modules: a dependency nothing installs
 * makes it typecheck nowhere rather than everywhere. Wrangler bundles without
 * consulting these, so they are for editors and tsc, and narrow on purpose
 * -- widening them means the code started using something new.
 */

interface R2HTTPMetadata {
  contentType?: string;
}

interface R2Object {
  key: string;
  size: number;
  uploaded: Date;
  httpMetadata?: R2HTTPMetadata;
  customMetadata?: Record<string, string>;
}

interface R2ObjectBody extends R2Object {
  body: ReadableStream | ArrayBuffer;
}

interface R2Bucket {
  put(
    key: string,
    value: ArrayBuffer | ReadableStream | string,
    options?: { httpMetadata?: R2HTTPMetadata; customMetadata?: Record<string, string> }
  ): Promise<R2Object>;
  get(key: string): Promise<R2ObjectBody | null>;
  list(options?: {
    prefix?: string;
    limit?: number;
    include?: string[];
    cursor?: string;
  }): Promise<{ objects: R2Object[]; truncated: boolean; cursor?: string }>;
}

interface ExportedHandler<E = unknown> {
  fetch(request: Request, env: E): Promise<Response>;
}

interface D1Result<T = Record<string, unknown>> {
  results: T[];
  success: boolean;
}

interface D1PreparedStatement {
  bind(...values: unknown[]): D1PreparedStatement;
  run(): Promise<D1Result>;
  all<T = Record<string, unknown>>(): Promise<D1Result<T>>;
}

interface D1Database {
  prepare(query: string): D1PreparedStatement;
}
