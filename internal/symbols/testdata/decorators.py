def simple_decorator(f):
    return f


def parametrized(arg):
    def wrap(f):
        return f
    return wrap


@simple_decorator
@parametrized("x")
def decorated_function():
    """Decorated function doc."""
    pass


class Widget:
    @simple_decorator
    @parametrized("y")
    @parametrized("z")
    def stacked_method(self):
        pass
