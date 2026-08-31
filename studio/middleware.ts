import { NextResponse, type NextRequest } from "next/server";
import { createServerClient } from "@supabase/ssr";

// Auth gate. Every page except /login and Next.js internals requires a
// valid Supabase session cookie. This is a UX guard, not the security
// boundary — Core enforces ownership via JWKS independently.
export async function middleware(request: NextRequest) {
  const url = process.env.NEXT_PUBLIC_SUPABASE_URL;
  const anon = process.env.NEXT_PUBLIC_SUPABASE_ANON_KEY;

  // Don't break local dev when env isn't set yet — let it through and
  // surface the misconfiguration in the login page.
  if (!url || !anon) return NextResponse.next();

  let response = NextResponse.next({ request });

  const supabase = createServerClient(url, anon, {
    cookies: {
      getAll() {
        return request.cookies.getAll();
      },
      setAll(items) {
        for (const { name, value } of items) request.cookies.set(name, value);
        response = NextResponse.next({ request });
        for (const { name, value, options } of items) {
          response.cookies.set(name, value, options);
        }
      },
    },
  });

  const {
    data: { user },
  } = await supabase.auth.getUser();

  const path = request.nextUrl.pathname;
  const isLogin = path === "/login" || path.startsWith("/login/");

  if (!user && !isLogin) {
    const redirectURL = request.nextUrl.clone();
    redirectURL.pathname = "/login";
    redirectURL.searchParams.set("from", path);
    return NextResponse.redirect(redirectURL);
  }
  if (user && isLogin) {
    const home = request.nextUrl.clone();
    home.pathname = request.nextUrl.searchParams.get("from") || "/";
    home.search = "";
    return NextResponse.redirect(home);
  }

  return response;
}

export const config = {
  // Skip Next internals + static files. The login page handles its own
  // redirect logic for already-authenticated users.
  matcher: ["/((?!_next/static|_next/image|favicon.ico|.*\\.png|.*\\.jpg|.*\\.svg).*)"],
};
