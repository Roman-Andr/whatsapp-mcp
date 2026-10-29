Fixtures, encoded with libopus (through PyAV, when the server was still in
Python); the decoder tests compare against them:

- `tone-voip16k.ogg`: 3 s of 440 Hz, 16 kHz input, `application=voip`, 24 kbit/s,
  the profile WhatsApp uses for voice notes (SILK).
- `tone-audio48k.ogg`: the same tone, 48 kHz, `application=audio`, 64 kbit/s (CELT).
- `jfk-voip16k.ogg`: whisper.cpp's `samples/jfk.wav` (11 s, MIT licence), 16 kHz,
  `voip`, 10 kbit/s.
