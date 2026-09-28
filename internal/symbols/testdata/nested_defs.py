"""Nested defs inside function bodies."""


def outer(x):
    """Outer doc."""
    local = 1

    def inner(y):
        def deep(z):
            return z

        return deep

    class Local:
        attr = 1

        def meth(self):
            def in_method():
                return 1

            return in_method

    return inner, Local


class K:
    def method(self):
        def helper():
            return 1

        return helper


def conditional(flag):
    if flag:

        def variant():
            return 1

    else:

        def variant():
            return 2

    for _ in range(2):

        def in_loop():
            return 0

    try:

        def in_try():
            return 0

    except ImportError:
        pass

    squares = [n * n for n in range(3)]
    f = lambda: None
    return variant, squares, f


def decorator_factory(times):
    def decorator(fn):
        @wraps(fn)
        async def wrapper(*a, **kw):
            return await fn(*a, **kw)

        return wrapper

    return decorator
