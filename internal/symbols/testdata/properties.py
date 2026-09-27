class Circle:
    def __init__(self, radius):
        self.radius = radius

    @property
    def area(self):
        """Area doc."""
        return 3.14 * self.radius ** 2

    @area.setter
    def area(self, value):
        pass
