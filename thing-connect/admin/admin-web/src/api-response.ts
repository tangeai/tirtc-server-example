import {
  problemFromAPIResponse,
  problemFromRequestFailure,
  type AdminProblem,
} from './error-feedback';

type APIResponseBody<T> = {
  code?: unknown;
  msg?: unknown;
  data?: T;
};

export class AdminAPIError extends Error {
  readonly problem: AdminProblem;

  constructor(problem: AdminProblem) {
    super(problem.message);
    this.name = 'AdminAPIError';
    this.problem = problem;
  }
}

export async function readAPIResponse<T>(path: string, response: Response): Promise<T> {
  let body: APIResponseBody<T>;
  try {
    body = (await response.json()) as APIResponseBody<T>;
  } catch (cause) {
    throw new AdminAPIError(problemFromRequestFailure('invalid-response', cause, response.status));
  }
  if (!response.ok || body.code !== 200) {
    throw new AdminAPIError(problemFromAPIResponse(path, response.status, body));
  }
  return body.data as T;
}

type AuthorizedFetch = (path: string, init?: RequestInit) => Promise<Response>;
type ErrorReporter = (error: AdminAPIError) => void;

export async function executeAdminRequest<T>(
  path: string,
  init: RequestInit,
  fetcher: AuthorizedFetch,
  report: ErrorReporter,
): Promise<T> {
  let response: Response;
  try {
    response = await fetcher(path, init);
  } catch (cause) {
    const error = new AdminAPIError(problemFromRequestFailure('network', cause));
    report(error);
    throw error;
  }
  try {
    return await readAPIResponse<T>(path, response);
  } catch (error) {
    const reported =
      error instanceof AdminAPIError
        ? error
        : new AdminAPIError(problemFromRequestFailure('invalid-response', error, response.status));
    report(reported);
    throw reported;
  }
}

export async function executeAdminDownload(
  path: string,
  fetcher: AuthorizedFetch,
  report: ErrorReporter,
): Promise<Blob> {
  let response: Response;
  try {
    response = await fetcher(path);
  } catch (cause) {
    const error = new AdminAPIError(problemFromRequestFailure('network', cause));
    report(error);
    throw error;
  }
  if (!response.ok) {
    try {
      await readAPIResponse(path, response);
    } catch (error) {
      const reported =
        error instanceof AdminAPIError
          ? error
          : new AdminAPIError(
              problemFromRequestFailure('invalid-response', error, response.status),
            );
      report(reported);
      throw reported;
    }
  }
  return response.blob();
}
