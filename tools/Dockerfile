FROM python:3.11-slim

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY serial_mux.py .

ENTRYPOINT ["python", "serial_mux.py"]
CMD ["--serial", "/dev/ttyUSB0", "--capture-port", "2001"]
