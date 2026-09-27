def long_signature(
    a,
    b=1,
    *args,
    c: int = 2,
    **kwargs,
) -> None:
    """Long signature doc."""
    pass


class Config:
    def __init__(
        self,
        name: str,
        value: int = 0,
    ):
        pass
