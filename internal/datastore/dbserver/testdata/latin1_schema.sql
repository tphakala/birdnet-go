-- Makes the container's schema default latin1, as on an install whose server
-- default charset is latin1.
ALTER DATABASE birdnet_test CHARACTER SET latin1 COLLATE latin1_swedish_ci;
