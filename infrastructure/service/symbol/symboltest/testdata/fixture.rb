# Billing namespace.
module Billing
  # Invoice is a bill.
  class Invoice
    def initialize(lines)
      @lines = lines
    end

    def total
      @lines.sum(&:price)
    end
  end
end

def overdue?(invoice)
  invoice.total > 0
end
